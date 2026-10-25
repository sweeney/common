# Roadmap for `sweeney/common`

This repo is the home for Go code shared across Sweeney services, as
**independent modules**: one per concern, each with its own `go.mod` and its
own path-prefixed tags. Code lands here only once more than one real consumer
shows what the general form is. Premature extraction is how shared libraries
get the wrong API and then break every consumer fixing it.

## Module 1: `iot` (landed, tagged `iot/v0.1.0`)

The engine for **GPIO on/off sensors** on a Pi: sub-packages `profile`,
`logic`, `gpio`, `mqtt`, `status`, plus the `Run` daemon runtime in the root
package. It is purely generic, with no boiler/CH/HW vocabulary. It builds and
tests clean (`logic` and `status` at 100% coverage) and cross-compiles for
`linux/arm`. A wire-contract self-test pins the legacy boiler bytes (2-channel
boiler-shaped profile) plus a 1-channel doorbell smoke test.

**Consumers:** `boiler-sensor` is the one live consumer. `doorbell-sensor` was
built against it to validate the API and is green, but it is parked
(deprioritised, 2026-10-25) and not published. The doorbell smoke test in this
module keeps the engine tested as generic in CI without it.

**Scope, as re-planned 2026-10-25:** `iot` is for edge-triggered on/off
inputs only. It is not a general MQTT library for the fleet:

- `netmon`, `ups-mqtt` and `tempest-mqtt` each grew their own publisher.
  `iot/mqtt` is bound to the sensor profile and payload shape, so it never
  fitted them.
- `tanktemp` (DS18B20 numeric readings, 4–8 s sweeps) gets its own loop rather
  than an extension here. Revisit a numeric reader once a second numeric
  sensor exists.

**Explicitly NOT in `iot`:** the per-binary `SensorProfile` (channel/pin/event
vocabulary, MQTT topics, payload root key, web title), and the HTTP/HTML status
page. Each sensor repo owns its own `internal/web`, consuming `iot/status` for
the snapshot and JSON. (The earlier open question of moving the page here was
to be settled "before a third sensor appears"; the third sensor, tanktemp, won't
use `iot/status`, so the page stays per repo.)

**Dependencies:** Paho MQTT + go-gpiocdev only. Targets 32-bit ARM (Pi Zero); no
CGO in test paths; keep the dependency graph minimal.

## Next candidates

Each lands as its own module, under its own trigger. None is part of the `iot`
work.

1. **Config-service client.** statehouse, countinghouse and greenhouse each
   carry their own copy of the config.swee.net Fetcher and of the
   `DeviceConfig` mirror of the `devices_<site>` namespace ("mirrors
   statehouse", "copied from statehouse"). Three real consumers make this the
   strongest candidate. It takes a token-source interface, as countinghouse's
   copy already does, so it does not depend on `identity`.
2. **MQTT retry/publisher.** `netmon` and `ups-mqtt` have near-identical
   `retry` packages and similar publishers (capped backoff, retained state,
   `{"online":false}` last will, telemetry QoS 0 / event QoS 1 behind a
   bridge). Extract once the two stop diverging.

## `identity/common`: move gradually, not in one go

`github.com/sweeney/identity/common` (`auth`, `apierr`, `backup`, `cli`, `db`,
`httputil`, `ratelimit`, `secrets`, `spec`) is at v0.6.0 with six consumers
(identity, config, countinghouse, greenhouse, mqttproxy, statehouse). Moving it
all here would be one breaking import-path change across six repos at once, so
we don't.

- **`auth` stays in identity.** It is identity's own client library.
- **The generic packages move one module at a time**, when a consumer is being
  changed anyway. The first such moment is bumping statehouse and greenhouse
  off v0.3.0.

## Sequencing summary

1. Generalize boiler-sensor in place (in its repo). ✅ done
2. Lift the engine here as module `iot` (purely generic). ✅ done
3. Migrate `boiler-sensor` onto `common/iot`; golden bytes unchanged. ✅ done
4. Build `doorbell-sensor` (own repo) against `common/iot`. ✅ done, then parked
5. Validate consumers green, tag `iot/v0.1.0` and pin it. ✅ done
6. Publish this repo and the tag; ship boiler-sensor on the tag. ⏳ in progress
7. Config-service client module (next candidate 1). Separate trigger.
8. Generic `identity/common` packages, one module at a time. Separate trigger.
