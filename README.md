# `sweeney/common`

Shared Go packages used across Sweeney services. This is a **multi-module repo**:
each top-level directory is its own Go module with its own `go.mod`, so a consumer
only pulls the dependencies of the module it actually imports.

> Why multi-module and not one module with subpackages: a single `go.mod` would
> carry the *union* of every dependency (AWS SDK, SQLite, JWT, …). A constrained
> consumer — e.g. a Raspberry Pi Zero daemon importing only `iot` — must not drag
> in the AWS SDK for code it never calls. Separate modules keep each consumer's
> dependency graph minimal.

## Modules

| Module | Import path | Status | Pulls in |
|---|---|---|---|
| `iot` | `github.com/sweeney/common/iot` | **landed — `iot/v0.1.0`** | Paho MQTT, go-gpiocdev only |
| config client | | candidate (copied today in statehouse, countinghouse, greenhouse) | stdlib only |
| `db` | `github.com/sweeney/common/db` | candidate (move from identity/common, one module at a time) | modernc sqlite |
| `backup` | `github.com/sweeney/common/backup` | candidate (move from identity/common, one module at a time) | AWS SDK (R2/S3) |
| … | | | (other generic identity/common packages; `auth` stays in identity) |

See [`PLAN.md`](PLAN.md) for what lands when and why.

## Versioning

Each module is tagged independently with a path-prefixed semver tag:

```
iot/v0.1.0
db/v1.0.0
```

Consumers pin an exact version (`go get github.com/sweeney/common/iot@v0.1.0`).
Never make a breaking change without bumping the module's minor (pre-1.0) or
major (post-1.0) version.

## Status

The first module, `iot`, has landed and is tagged `iot/v0.1.0`. It is the
sensor-agnostic engine extracted from `boiler-sensor`, purely generic (no
boiler/CH/HW vocabulary). Packages:

- `iot/profile` — `Channel`/`SensorProfile`: the per-binary config (channels,
  pins, events, topics, payload root, web title) that drives everything.
- `iot/logic` — channel-keyed debounce/baseline/heartbeat `Detector`
  (`NewProfileDetector`/`ProcessValues`/`GenericEvent`); zero gpio/mqtt/os
  imports, time injected, 100% test coverage.
- `iot/gpio` — generic `ChannelReader` (real Linux `go-gpiocdev` +
  `FakeChannelReader` + non-Linux stub); per-channel active-low inversion is the
  only place inversion happens.
- `iot/mqtt` — the resilient publisher (reconnect with capped backoff, offline
  ring buffer, last-will, RECONNECTED notice) plus the ordered
  `FormatEventPayload` and system-payload formatting.
- `iot/status` — thread-safe N-channel tracker with an ordered (profile-order)
  JSON encoder (shared by the MQTT heartbeat and each service's status page).
- `iot` (root) — `Run`: the daemon runtime shared by every sensor binary. Polls
  the reader, debounces, publishes transition events and lifecycle messages
  (STARTUP / ready+scheduled HEARTBEAT / SHUTDOWN), handles signals, and keeps
  the tracker updated. Eliminates the per-binary poll-loop boilerplate.

The HTTP/HTML status **UI is intentionally not in `iot`** — it's service-specific
presentation, so each sensor binary rolls its own (see each repo's `internal/web`).
`iot` stays focused on the engine + runtime.

A wire-contract self-test (`iot/wire_contract_test.go`) proves a 2-channel
boiler-shaped profile reproduces the exact legacy boiler bytes, plus a 1-channel
doorbell-shaped smoke test; `iot/runner_test.go` covers the runtime lifecycle.
boiler-sensor is the live consumer; doorbell-sensor validated the API and is
parked. `iot` is scoped to GPIO on/off sensors. See `PLAN.md`.
