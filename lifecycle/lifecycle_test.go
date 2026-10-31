package lifecycle

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// harness drives Run with hand-fed channels and records every event.
type harness struct {
	tick   chan time.Time
	hb     chan time.Time
	sig    chan os.Signal
	mu     sync.Mutex
	events []Event
	skips  int
	done   chan error
}

var t0 = time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC)

func start(t *testing.T, ctx context.Context, cfg Config) *harness {
	t.Helper()
	h := &harness{
		tick: make(chan time.Time),
		hb:   make(chan time.Time),
		sig:  make(chan os.Signal, 1),
		done: make(chan error, 1),
	}
	cfg.Tick, cfg.HeartbeatTick, cfg.Signals = h.tick, h.hb, h.sig
	cfg.Now = func() time.Time { return t0 }
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.Emit = func(e Event) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.events = append(h.events, e)
		return nil
	}
	cfg.OnSkip = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.skips++
	}
	go func() { h.done <- Run(ctx, cfg) }()
	return h
}

func (h *harness) kinds() []Kind {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Kind
	for _, e := range h.events {
		out = append(out, e.Kind)
	}
	return out
}

// waitFor polls until the events match, or fails.
func (h *harness) waitFor(t *testing.T, want ...Kind) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if equal(h.kinds(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("events = %v, want %v", h.kinds(), want)
}

func equal(a, b []Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (h *harness) stop(t *testing.T, s os.Signal) {
	t.Helper()
	h.sig <- s
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the signal")
	}
}

func TestLifecycle(t *testing.T) {
	var ready bool
	var mu sync.Mutex
	works := 0
	h := start(t, context.Background(), Config{
		Work: func(ctx context.Context, now time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			works++
			ready = true
			return nil
		},
		Ready: func() bool { mu.Lock(); defer mu.Unlock(); return ready },
	})
	h.waitFor(t, Startup)
	h.hb <- t0 // a heartbeat before ready is not sent
	h.tick <- t0
	h.waitFor(t, Startup, Ready)
	h.tick <- t0 // Ready is sent once
	h.hb <- t0
	h.waitFor(t, Startup, Ready, Heartbeat)
	h.stop(t, syscall.SIGTERM)
	h.waitFor(t, Startup, Ready, Heartbeat, Shutdown)
	if r := h.events[3].Reason; r != "SIGTERM" {
		t.Errorf("reason = %q", r)
	}
	if !h.events[0].At.Equal(t0) {
		t.Errorf("At = %v", h.events[0].At)
	}
	if works != 2 {
		t.Errorf("work ran %d times, want 2", works)
	}
}

func TestImmediateRunsWorkBeforeTheFirstTick(t *testing.T) {
	ran := make(chan struct{}, 1)
	h := start(t, context.Background(), Config{
		Immediate: true,
		Work:      func(context.Context, time.Time) error { ran <- struct{}{}; return nil },
		Ready:     func() bool { return true },
	})
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("work did not run without a tick")
	}
	h.waitFor(t, Startup, Ready)
	h.stop(t, os.Interrupt)
	if h.events[2].Reason != "SIGINT" {
		t.Errorf("reason = %q", h.events[2].Reason)
	}
}

func TestATickDuringWorkIsSkippedNotQueued(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 10)
	h := start(t, context.Background(), Config{
		Work: func(ctx context.Context, now time.Time) error {
			started <- struct{}{}
			<-release
			return nil
		},
		Ready: func() bool { return true },
	})
	h.tick <- t0
	<-started
	h.tick <- t0
	h.tick <- t0
	close(release)
	h.waitFor(t, Startup, Ready)
	h.mu.Lock()
	skips := h.skips
	h.mu.Unlock()
	if skips != 2 {
		t.Errorf("skips = %d, want 2", skips)
	}
	select {
	case <-started:
		t.Error("a skipped tick ran work later")
	case <-time.After(20 * time.Millisecond):
	}
	h.stop(t, syscall.SIGTERM)
}

func TestShutdownMidWorkCancelsAndWaits(t *testing.T) {
	started := make(chan struct{})
	var finished bool
	var mu sync.Mutex
	h := start(t, context.Background(), Config{
		Work: func(ctx context.Context, now time.Time) error {
			close(started)
			<-ctx.Done() // a long sweep that honours cancellation
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			finished = true
			mu.Unlock()
			return ctx.Err()
		},
		Ready: func() bool { return false },
	})
	h.tick <- t0
	<-started
	begin := time.Now()
	h.stop(t, syscall.SIGTERM)
	if time.Since(begin) > time.Second {
		t.Error("shutdown waited too long")
	}
	mu.Lock()
	defer mu.Unlock()
	if !finished {
		t.Error("Shutdown was emitted before the work returned")
	}
	h.waitFor(t, Startup, Shutdown)
}

func TestContextCancelIsAShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := start(t, ctx, Config{Work: func(context.Context, time.Time) error { return nil }, Ready: func() bool { return false }})
	h.waitFor(t, Startup)
	cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no return on cancel")
	}
	h.waitFor(t, Startup, Shutdown)
	if h.events[1].Reason != "context" {
		t.Errorf("reason = %q", h.events[1].Reason)
	}
}

func TestErrorsAreLoggedNotFatal(t *testing.T) {
	calls := 0
	tick := make(chan time.Time)
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)
	var mu sync.Mutex
	var kinds []Kind
	go func() {
		done <- Run(context.Background(), Config{
			Work: func(context.Context, time.Time) error {
				mu.Lock()
				calls++
				mu.Unlock()
				return errors.New("sweep failed")
			},
			Ready: func() bool { return true },
			Emit: func(e Event) error {
				mu.Lock()
				kinds = append(kinds, e.Kind)
				mu.Unlock()
				return errors.New("broker down")
			},
			Tick: tick, HeartbeatTick: make(chan time.Time), Signals: sig,
			Logger: log.New(io.Discard, "", 0),
		})
	}()
	tick <- t0
	tick <- t0
	sig <- syscall.SIGTERM
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 1 || len(kinds) < 2 || kinds[len(kinds)-1] != Shutdown {
		t.Errorf("calls %d kinds %v", calls, kinds)
	}
}

func TestConfigErrors(t *testing.T) {
	if err := Run(context.Background(), Config{}); !errors.Is(err, ErrConfig) {
		t.Errorf("err = %v", err)
	}
	if err := Run(context.Background(), Config{Work: func(context.Context, time.Time) error { return nil }, Emit: func(Event) error { return nil }}); !errors.Is(err, ErrConfig) {
		t.Errorf("no interval and no tick: err = %v", err)
	}
}

func TestDefaultTickers(t *testing.T) {
	// With real tickers: work runs on Interval, a heartbeat on Heartbeat.
	var mu sync.Mutex
	var kinds []Kind
	works := 0
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{
			Interval:  5 * time.Millisecond,
			Heartbeat: 20 * time.Millisecond,
			Work:      func(context.Context, time.Time) error { mu.Lock(); works++; mu.Unlock(); return nil },
			Ready:     func() bool { return true },
			Emit:      func(e Event) error { mu.Lock(); kinds = append(kinds, e.Kind); mu.Unlock(); return nil },
			Signals:   sig,
			Logger:    log.New(io.Discard, "", 0),
		})
	}()
	time.Sleep(60 * time.Millisecond)
	sig <- syscall.SIGTERM
	<-done
	mu.Lock()
	defer mu.Unlock()
	hb := 0
	for _, k := range kinds {
		if k == Heartbeat {
			hb++
		}
	}
	if works < 3 || hb < 1 {
		t.Errorf("works %d heartbeats %d (%v)", works, hb, kinds)
	}
}

func TestKindString(t *testing.T) {
	for k, s := range map[Kind]string{Startup: "STARTUP", Ready: "READY", Heartbeat: "HEARTBEAT", Shutdown: "SHUTDOWN", Kind(99): "Kind(99)"} {
		if k.String() != s {
			t.Errorf("%d = %s, want %s", int(k), k.String(), s)
		}
	}
}

func TestSignalName(t *testing.T) {
	for s, want := range map[os.Signal]string{syscall.SIGTERM: "SIGTERM", os.Interrupt: "SIGINT", syscall.SIGHUP: "hangup"} {
		if got := signalName(s); got != want {
			t.Errorf("%v = %s, want %s", s, got, want)
		}
	}
}
