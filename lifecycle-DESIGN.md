# Design: `lifecycle`, the shared daemon runtime

Status: **built locally, not tagged, not pushed** (2026-10-31).

## Why

`iot.Run` (`iot/runner.go`) mixes two things: the lifecycle (signals,
STARTUP, the ready heartbeat, scheduled heartbeats, SHUTDOWN with a reason)
and GPIO on/off work. tanktemp, the DS18B20 hot water service, needs the
lifecycle but not the GPIO work. Its work is a 4–8 s sweep that `iot.Run`'s
inline 100 ms poll can't host: shutdown would wait behind a read. Copying the
lifecycle into tanktemp would make a second copy, so it moves here, with no
dependencies.

## Decisions

1. **Its own module, stdlib only.** It is not part of `iot`, which stays
   GPIO-scoped, and not part of `mqttpub`, because the lifecycle isn't tied
   to a transport.
2. **No payload shapes.** `Emit(Event)` hands over the kind, the time and
   the reason, and the caller does the rest. boiler-sensor's
   `{"system":…}`/`{"status":…}` and tanktemp's `{"online":…}` both fit.
3. **The work runs in a goroutine with a cancellable context.** A signal is
   handled at once. Shutdown cancels the work, waits for it, then emits.
4. **Ticks are skipped, never queued,** while work is running.
5. **Heartbeats only once ready,** as in `iot.Run`, where a scheduled
   heartbeat only fires once baselined.

## Consumers and tags

1. tanktemp, first, through a gitignored `go.work`.
2. **`iot` rebuilt on it (`iot/v0.2.0`): not done. Tried against `iot`'s tests
   on 2026-10-31, and two incompatibilities are now known:**
   - **Skipped ticks.** `iot`'s `runScenario` sends ticks back to back on an
     unbuffered channel and expects each to run one poll. With the work in
     its own goroutine, a tick that arrives while the previous poll is still
     finishing is skipped, so the scenarios would see fewer polls.
   - **The fake clock.** `iot`'s `fakeClock` advances 100 ms per call and
     isn't synchronised, because only the runtime goroutine calls it. Debounce
     outcomes depend on the number of `Now()` calls. `lifecycle` calls
     `Now()` in different places (Startup, every unit of work, Ready,
     Shutdown), and from the work goroutine too.

   Likely fix: an `Inline` option that runs `Work` on the loop goroutine,
   for work that is always quick (a 100 ms GPIO poll), with `Now()` called
   exactly where `iot.Run` calls it today. The heartbeat schedule then stays
   in `iot`'s detector, called from `Work`, with lifecycle's `Heartbeat` set
   to 0. In both cases `iot`'s `runner_test.go` and `wire_contract_test.go`,
   and boiler-sensor's `lifecycle_golden_test.go`, must pass unchanged.
3. Tag `lifecycle/v0.1.0` once tanktemp has run on it on a Pi. Moving
   boiler-sensor is tracked in sweeney/boiler-sensor#6.

Later candidates: ups-mqtt and netmon, whose daemons have the same shape.
