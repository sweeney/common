# `github.com/sweeney/common/lifecycle`

The runtime around a daemon's work. Stdlib only. It handles SIGINT and
SIGTERM, runs the work on a schedule with a cancellable context, and
announces the lifecycle moments. It knows nothing about MQTT, GPIO, sensors
or payloads: the caller's `Emit` formats and publishes each event.

```go
err := lifecycle.Run(ctx, lifecycle.Config{
	Work:      func(ctx context.Context, now time.Time) error { return sweep(ctx) },
	Ready:     func() bool { return haveFirstSweep() },
	Emit:      func(e lifecycle.Event) error { return publish(e) },
	Interval:  30 * time.Second,
	Heartbeat: 15 * time.Minute,
	Immediate: true,
})
```

| Event | When |
|---|---|
| `Startup` | Run has started, before any work |
| `Ready` | the first time `Ready()` is true after a unit of work; once |
| `Heartbeat` | on the heartbeat schedule, once ready |
| `Shutdown` | a signal or a cancelled `ctx`, **after the work has returned**; `Reason` is `SIGTERM`, `SIGINT` or `context` |

The work runs in its own goroutine, so a signal is handled at once even
during slow work: the work's context is cancelled, Run waits for it, then
emits `Shutdown`. A tick that arrives while work is still running is skipped
and reported through `OnSkip`, never queued. `Emit` and `Work` errors are
logged, never fatal.

`Now`, `Tick`, `HeartbeatTick`, `Signals` and `Logger` can all be injected,
so a consumer can test its wiring deterministically.

See [`lifecycle-DESIGN.md`](../lifecycle-DESIGN.md).
