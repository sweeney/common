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

## Modules 2–4: `db`, `backup`, `web` (moved from `identity/common`)

The generic service-infrastructure packages of
`github.com/sweeney/identity/common` move here as of its `common/v0.6.0`,
imported **with their history** (from identity's initial commit; PR references
rewritten to `sweeney/identity#N`). Split by dependency weight:

| Module | Packages (was `identity/common/…`) | Pulls in | Consumers |
|---|---|---|---|
| `db` | `db` | modernc sqlite | identity, config, countinghouse |
| `backup` | `backup`, `backup/cli` (was `cli`), `backup/mocks` | AWS SDK, sqlite | identity, config, countinghouse |
| `web` | `apierr`, `httputil`, `ratelimit` | `x/time` | identity, config |

`httputil` moves with `ratelimit`, which keys on `httputil.ExtractClientIP`.
`apierr` is 12 lines, so it rides in the light `web` module.

**A pure move.** No code changes beyond import paths; dependency versions are
pinned to identity/common v0.6.0's. Every test comes along and passes unchanged
(167 of 167). The `schema_migrations` table and its contents and backup's R2
key layout (`ServiceName` still defaults to `"identity"`) are unchanged, so a
deployed database or bucket needs nothing.

**Staying in `identity/common`:** `auth` (identity's own client library; moving
it here too is an open option, easiest once it is nearly all that is left),
`spec`, and `secrets` (identity-only; will import `sweeney/common/db`).

**Consumer status (2026-10-28):** config, countinghouse and statehouse on
identity/common v0.6.0; mqttproxy v0.4.0; greenhouse v0.3.0. greenhouse,
statehouse and mqttproxy import only `auth`/`spec` and need no import changes;
greenhouse should still come up to v0.6.0 for the September security fixes.

**Order of work** (each step ends green):

1. This repo: land the three modules and tag `db/v0.1.0`, `backup/v0.1.0`,
   `web/v0.1.0`.
2. identity: switch to the new modules, delete the moved packages from
   `identity/common`, point `secrets` at `common/db`, tag `identity/common`
   v0.7.0 (a minor bump: packages removed). v0.6.0 stays valid meanwhile.
3. config: move to the new modules + identity/common v0.7.0.
4. countinghouse: the same.
5. greenhouse, statehouse, mqttproxy: bump to v0.7.0 when next touched; the AWS
   SDK leaves their module graph. sqlite stays (via `secrets`) unless `auth`
   gets its own `go.mod`.

## Sequencing summary

1. Generalize boiler-sensor in place (in its repo). ✅ done
2. Lift the engine here as module `iot` (purely generic). ✅ done
3. Migrate `boiler-sensor` onto `common/iot`; golden bytes unchanged. ✅ done
4. Build `doorbell-sensor` (own repo) against `common/iot`. ✅ done, then parked
5. Validate consumers green, tag `iot/v0.1.0` and pin it. ✅ done
6. Publish this repo and the tag; ship boiler-sensor on the tag. ⏳ in progress
7. Move `db`, `backup`, `web` here from `identity/common`, then migrate
   identity, config and countinghouse. ⏳ in progress
8. Config-service client module (next candidate 1). Separate trigger.
