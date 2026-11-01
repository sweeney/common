// Package lifecycle is the runtime around a daemon's work: it handles
// signals, runs the work on a schedule with a cancellable context, and
// announces STARTUP, the first ready moment, scheduled heartbeats and
// SHUTDOWN. It knows nothing about MQTT, GPIO, sensors or payload shapes:
// the caller's Emit formats and publishes each Event.
//
// The point of running the work in its own goroutine is that a signal is
// handled at once even while slow work (a 4–8 s 1-Wire sweep) is in
// progress: the work's context is cancelled, Run waits for it to return,
// and only then emits SHUTDOWN. A tick that arrives while work is still
// running is skipped (and reported through OnSkip), never queued.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Kind is a lifecycle moment.
type Kind int

const (
	// Startup: Run has started, before any work.
	Startup Kind = iota
	// Ready: the first time Ready() is true after a unit of work. Emitted
	// once; consumers usually publish it as their first heartbeat.
	Ready
	// Heartbeat: a scheduled heartbeat, only once ready.
	Heartbeat
	// Shutdown: a signal or a cancelled context; the work has stopped.
	Shutdown
)

func (k Kind) String() string {
	switch k {
	case Startup:
		return "STARTUP"
	case Ready:
		return "READY"
	case Heartbeat:
		return "HEARTBEAT"
	case Shutdown:
		return "SHUTDOWN"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Event is passed to Emit.
type Event struct {
	Kind   Kind
	At     time.Time
	Reason string // Shutdown only: "SIGTERM", "SIGINT", another signal's name, or "context"
}

// Config wires Run. Work and Emit are required, and either Interval or
// Tick. The injection points default to production behaviour.
type Config struct {
	// Work does one unit of work. It must return promptly once ctx is done.
	Work func(ctx context.Context, now time.Time) error
	// Ready reports whether the daemon has something real to report yet.
	// Nil means always ready.
	Ready func() bool
	// Emit publishes an event. Errors are logged, never fatal.
	Emit func(Event) error

	Interval  time.Duration // work cadence, when Tick is nil
	Heartbeat time.Duration // heartbeat cadence, when HeartbeatTick is nil; 0 means none
	Immediate bool          // run Work once at start rather than waiting for the first tick
	OnSkip    func()        // a tick arrived while Work was still running

	Now           func() time.Time // default time.Now
	Tick          <-chan time.Time // default a ticker on Interval
	HeartbeatTick <-chan time.Time // default a ticker on Heartbeat
	Signals       <-chan os.Signal // default SIGINT and SIGTERM
	Logger        *log.Logger      // default log.Default()
}

// ErrConfig: a required field is missing.
var ErrConfig = errors.New("lifecycle: bad config")

// Run emits Startup, runs Work on every tick, emits Ready and Heartbeats,
// and on a signal or a cancelled ctx stops the work and emits Shutdown. It
// returns nil after a clean shutdown.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Work == nil || cfg.Emit == nil {
		return fmt.Errorf("%w: Work and Emit are required", ErrConfig)
	}
	if cfg.Tick == nil && cfg.Interval <= 0 {
		return fmt.Errorf("%w: Interval or Tick is required", ErrConfig)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	ready := cfg.Ready
	if ready == nil {
		ready = func() bool { return true }
	}
	sig := cfg.Signals
	if sig == nil {
		sc := make(chan os.Signal, 1)
		signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sc)
		sig = sc
	}
	tick := cfg.Tick
	if tick == nil {
		t := time.NewTicker(cfg.Interval)
		defer t.Stop()
		tick = t.C
	}
	hbTick := cfg.HeartbeatTick
	if hbTick == nil && cfg.Heartbeat > 0 {
		t := time.NewTicker(cfg.Heartbeat)
		defer t.Stop()
		hbTick = t.C
	}

	emit := func(e Event) {
		if err := cfg.Emit(e); err != nil {
			logger.Printf("lifecycle: emit %s: %v", e.Kind, err)
		}
	}
	emit(Event{Kind: Startup, At: now()})

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var done chan error // non-nil while work is running
	startWork := func() {
		done = make(chan error, 1)
		go func(c chan error) { c <- cfg.Work(workCtx, now()) }(done)
	}
	if cfg.Immediate {
		startWork()
	}
	readySent := false
	shutdown := func(reason string) error {
		cancel()
		if done != nil {
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				logger.Printf("lifecycle: work during shutdown: %v", err)
			}
		}
		emit(Event{Kind: Shutdown, At: now(), Reason: reason})
		return nil
	}

	for {
		select {
		case s := <-sig:
			logger.Printf("lifecycle: received %v, shutting down", s)
			return shutdown(signalName(s))
		case <-ctx.Done():
			return shutdown("context")
		case <-tick:
			if done != nil {
				if cfg.OnSkip != nil {
					cfg.OnSkip()
				}
				continue
			}
			startWork()
		case err := <-done:
			done = nil
			if err != nil {
				logger.Printf("lifecycle: work: %v", err)
			}
			if !readySent && ready() {
				readySent = true
				emit(Event{Kind: Ready, At: now()})
			}
		case <-hbTick:
			if readySent {
				emit(Event{Kind: Heartbeat, At: now()})
			}
		}
	}
}

func signalName(s os.Signal) string {
	switch s {
	case syscall.SIGTERM:
		return "SIGTERM"
	case os.Interrupt:
		return "SIGINT"
	}
	return s.String()
}
