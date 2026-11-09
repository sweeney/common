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
	"testing/synctest"
	"time"
)

// Config.Next: the caller schedules the work (an adaptive cadence). These
// run inside a synctest bubble with the real clock, so time is virtual and
// exact: time.Sleep moves it, and synctest.Wait lets every goroutine settle
// before a test looks.

// nextRig runs Run with Next and records each unit of work's start (by the
// bubble's clock) and each call to Next.
type nextRig struct {
	sig  chan os.Signal
	done chan error

	mu      sync.Mutex
	starts  []time.Time
	calls   []nextCall
	skips   int
	running bool // a unit of work is in progress
	events  []Kind
}

type nextCall struct {
	started time.Time
	err     error
	during  bool // Next was called while work was running
}

// startNext runs Run with work that takes took(i) and returns errAt(i), and
// a Next that returns the start of the unit before it plus gap(i).
func startNext(t *testing.T, cfg Config, took func(i int) time.Duration, errAt func(i int) error,
	next func(started time.Time, err error) time.Time) *nextRig {
	t.Helper()
	r := &nextRig{sig: make(chan os.Signal, 1), done: make(chan error, 1)}
	cfg.Signals = r.sig
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.Emit = func(e Event) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, e.Kind)
		return nil
	}
	cfg.OnSkip = func() { r.mu.Lock(); r.skips++; r.mu.Unlock() }
	cfg.Work = func(ctx context.Context, now time.Time) error {
		r.mu.Lock()
		i := len(r.starts)
		r.starts = append(r.starts, time.Now())
		r.running = true
		r.mu.Unlock()
		defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()
		select {
		case <-time.After(took(i)):
		case <-ctx.Done():
			return ctx.Err()
		}
		return errAt(i)
	}
	cfg.Next = func(started time.Time, err error) time.Time {
		r.mu.Lock()
		r.calls = append(r.calls, nextCall{started: started, err: err, during: r.running})
		r.mu.Unlock()
		return next(started, err)
	}
	go func() { r.done <- Run(context.Background(), cfg) }()
	return r
}

func (r *nextRig) stop(t *testing.T) {
	t.Helper()
	r.sig <- syscall.SIGTERM
	if err := <-r.done; err != nil {
		t.Fatal(err)
	}
}

func (r *nextRig) snapshot() ([]time.Time, []nextCall, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.starts...), append([]nextCall(nil), r.calls...), r.skips
}

func never(int) error { return nil }

// Each unit starts at the time Next returned for it, and Next is told when
// the unit before started and what it returned, never while work runs.
func TestNextSchedulesTheWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		begin := time.Now()
		gaps := []time.Duration{30 * time.Second, 10 * time.Second, 10 * time.Second, 17 * time.Second}
		boom := errors.New("boom")
		n := 0 // Next is never called concurrently, so a plain counter will do
		r := startNext(t, Config{Immediate: true},
			func(int) time.Duration { return 4 * time.Second },
			func(i int) error {
				if i == 1 {
					return boom
				}
				return nil
			},
			func(started time.Time, _ error) time.Time {
				g := gaps[min(len(gaps)-1, n)]
				n++
				return started.Add(g)
			})
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		r.stop(t)

		starts, calls, skips := r.snapshot()
		want := []time.Duration{0, 30 * time.Second, 40 * time.Second, 50 * time.Second, 67 * time.Second, 84 * time.Second, 101 * time.Second, 118 * time.Second}
		if len(starts) != len(want) {
			t.Fatalf("%d units, want %d: %v", len(starts), len(want), offsets(starts, begin))
		}
		for i, w := range want {
			if got := starts[i].Sub(begin); got != w {
				t.Errorf("unit %d started at +%v, want +%v", i, got, w)
			}
		}
		for i, c := range calls {
			if c.during {
				t.Errorf("Next call %d came while work was running", i)
			}
			if !c.started.Equal(starts[i]) {
				t.Errorf("Next call %d: started %v, want unit %d's start %v", i, c.started.Sub(begin), i, starts[i].Sub(begin))
			}
		}
		if len(calls) < 2 || !errors.Is(calls[1].err, boom) || calls[0].err != nil {
			t.Errorf("Next wasn't told each unit's error: %+v", calls)
		}
		if skips != 0 {
			t.Errorf("OnSkip called %d times: with Next there are no ticks to skip", skips)
		}
	})
}

func offsets(ts []time.Time, begin time.Time) []time.Duration {
	var out []time.Duration
	for _, t := range ts {
		out = append(out, t.Sub(begin))
	}
	return out
}

// A due time that has already passed (the work overran) starts the next
// unit at once. Next decides what an overrun means; Run never queues.
func TestNextInThePastStartsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		begin := time.Now()
		r := startNext(t, Config{Immediate: true},
			func(int) time.Duration { return 12 * time.Second }, never,
			func(started time.Time, _ error) time.Time { return started.Add(10 * time.Second) })
		time.Sleep(30 * time.Second)
		synctest.Wait()
		r.stop(t)
		starts, _, _ := r.snapshot()
		if got := offsets(starts, begin); len(got) < 3 || got[1] != 12*time.Second || got[2] != 24*time.Second {
			t.Errorf("units started at %v, want 0, 12s, 24s (each as the last finished)", got)
		}
	})
}

// Without Immediate, Run asks Next for the first unit's time, with a zero
// started time.
func TestNextWithoutImmediate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		begin := time.Now()
		r := startNext(t, Config{},
			func(int) time.Duration { return time.Second }, never,
			func(started time.Time, _ error) time.Time {
				if started.IsZero() {
					return begin.Add(5 * time.Second)
				}
				return started.Add(time.Minute)
			})
		time.Sleep(90 * time.Second)
		synctest.Wait()
		r.stop(t)
		starts, calls, _ := r.snapshot()
		if got := offsets(starts, begin); len(got) != 2 || got[0] != 5*time.Second || got[1] != 65*time.Second {
			t.Errorf("units started at %v, want 5s and 65s", got)
		}
		if len(calls) == 0 || !calls[0].started.IsZero() {
			t.Errorf("first Next call %+v, want a zero started time", calls)
		}
	})
}

// A signal while Run waits for the next unit shuts down at once: no more
// work, and Shutdown is the last event.
func TestNextShutdownWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startNext(t, Config{Immediate: true},
			func(int) time.Duration { return time.Second }, never,
			func(started time.Time, _ error) time.Time { return started.Add(time.Hour) })
		time.Sleep(10 * time.Second)
		synctest.Wait()
		begin := time.Now()
		r.stop(t)
		if took := time.Since(begin); took != 0 {
			t.Errorf("shutdown took %v of virtual time", took)
		}
		starts, _, _ := r.snapshot()
		r.mu.Lock()
		last := r.events[len(r.events)-1]
		r.mu.Unlock()
		if len(starts) != 1 || last != Shutdown {
			t.Errorf("%d units, last event %v; want 1 and SHUTDOWN", len(starts), last)
		}
	})
}

// Ready and heartbeats work as they do with a ticker.
func TestNextKeepsReadyAndHeartbeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startNext(t, Config{Immediate: true, Heartbeat: time.Minute},
			func(int) time.Duration { return time.Second }, never,
			func(started time.Time, _ error) time.Time { return started.Add(20 * time.Second) })
		time.Sleep(150 * time.Second)
		synctest.Wait()
		r.stop(t)
		r.mu.Lock()
		defer r.mu.Unlock()
		want := []Kind{Startup, Ready, Heartbeat, Heartbeat, Shutdown}
		if !equal(r.events, want) {
			t.Errorf("events = %v, want %v", r.events, want)
		}
	})
}

// Next and Tick are two schedules: both at once is a config error. Next on
// its own needs no Interval.
func TestNextConfig(t *testing.T) {
	work := func(context.Context, time.Time) error { return nil }
	emit := func(Event) error { return nil }
	next := func(time.Time, error) time.Time { return time.Time{} }
	err := Run(context.Background(), Config{Work: work, Emit: emit, Next: next, Tick: make(chan time.Time)})
	if !errors.Is(err, ErrConfig) {
		t.Errorf("Next with Tick: err = %v, want ErrConfig", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, Config{Work: work, Emit: emit, Next: next, Signals: make(chan os.Signal), Logger: log.New(io.Discard, "", 0)}); err != nil {
		t.Errorf("Next without Interval: err = %v, want nil", err)
	}
}
