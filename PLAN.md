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

**Consumers:** `boiler-sensor` is the one live consumer, shipped on
`iot/v0.1.0`. A golden test in that repo drives `iot.Run` with the boiler
profile and pins every payload to what the pre-extraction build published, and
its deploy verifies `/healthz` and the broker heartbeat and rolls back on
failure. `doorbell-sensor` was built against it to validate the API and is
green, but it is parked (deprioritised, 2026-10-25) and not published. The doorbell smoke test in this
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
   copy already does, so it does not depend on `identity`. pi-price-screen's
   `rulesconfig` is a fourth copy. **Phase 1 landed as `remoteconfig/v0.1.0`**
   (sweeney/common#3, 3bd16f3); see
   [remoteconfig-DESIGN.md](remoteconfig-DESIGN.md). Consumers, one at a
   time, each deployed and verified before the next:

   | Consumer | Status |
   |---|---|
   | greenhouse | ported (35409cd), not yet pushed |
   | countinghouse | ported (1127f28), not yet pushed |
   | statehouse | after those two are deployed |
   | pi-price-screen | after statehouse |

   greenhouse's cold-boot refusal follows its port as its own PR. Phase 2
   (sharing the `devices_<site>` schema) waits until every phase-1 consumer
   has shipped, and gets its own design first.
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
| `web` | `apierr`, `httputil`, `ratelimit`; `spec` from `web/v0.2.0` | `x/time`, `yaml.v3` | identity, config; `spec` also countinghouse, greenhouse, statehouse |

`httputil` moves with `ratelimit`, which keys on `httputil.ExtractClientIP`.
`apierr` (12 lines) and `spec` (32 lines, OpenAPI YAML→JSON) ride in the light
`web` module rather than getting modules of their own.

**A pure move.** No code changes beyond import paths; dependency versions are
pinned to identity/common v0.6.0's. Every test comes along and passes unchanged
(167 of 167). The `schema_migrations` table and its contents and backup's R2
key layout (`ServiceName` still defaults to `"identity"`) are unchanged, so a
deployed database or bucket needs nothing.

**Staying in `identity/common`: only `auth`.** It changes in lockstep with
identity's token issuer (3 of its 9 commits also changed `internal/auth/jwt.go`,
and identity's tests check issued tokens against the verifier), so it stays
beside the server. Decided 2026-10-28; the user would still consider moving it.
`secrets` has no users outside identity, so it moves into identity's
`internal/` rather than here.

**Consumer status: migration complete.** Every consumer is on
the new modules and identity/common v0.7.0, and each is deployed:

| Consumer | Change | PR | Deployed |
|---|---|---|---|
| identity | uses `db`, `backup`, `web`; `identity/common` cut to `auth` | sweeney/identity#48 | 88c5b83, 2026-10-28 |
| config | `db`, `backup`, `web` + v0.7.0 | sweeney/config#6 | 35de216, 2026-10-28 |
| countinghouse | `db`, `backup`, `web/spec` + v0.7.0 | sweeney/countinghouse#58 | 20904de, 2026-10-28 |
| greenhouse | `web/spec` + v0.7.0 (from v0.3.0), 503 on `ErrKeysUnavailable` | sweeney/greenhouse#38 | 300d373, 2026-10-28 |
| statehouse | `web/spec` + v0.7.0 | sweeney/statehouse#76 | 00bca8b, 2026-10-28 |
| mqttproxy | v0.7.0 (from v0.4.0), `auth` only | sweeney/mqttproxy#1 | 6823fbe |

The `schema_migrations` ledgers and R2 key layouts are unchanged everywhere.
identity's deploy also brought its own ledger adoption (sweeney/identity#47),
which was rehearsed against a snapshot of production first (sweeney/identity#50).
greenhouse, statehouse and mqttproxy no longer pull in the AWS SDK or sqlite.

Follow-ups found along the way:

- `Retry-After: 0` from `web/ratelimit` at rates of 2/s and above
  (sweeney/common#1): fixed in `web/v0.2.1` (sweeney/common#2). identity
  (sweeney/identity#51, eeb4e93) and config (sweeney/config#8, 0ab6e31) are on
  it and deployed; their 2/s and 5/s limiters now send `1`.
- Audience enforcement for countinghouse and greenhouse, warn-only first:
  sweeney/countinghouse#59, sweeney/greenhouse#39.

**Order of work** (each step ends green):

1. This repo: land the three modules and tag `db/v0.1.0`, `backup/v0.1.0`,
   `web/v0.1.0` ✅; add `spec` as `web/v0.2.0` ✅.
2. identity: switch to the new modules, delete the moved packages from
   `identity/common`, move `secrets` into `internal/` (importing `common/db`),
   tag `identity/common` v0.7.0 (a minor bump: packages removed). It then holds
   only `auth`, depending on jwt and `x/sync`. v0.6.0 stays valid meanwhile. ✅
   (sweeney/identity#48, tagged `common/v0.7.0`)
3. config: move to the new modules + identity/common v0.7.0. ✅
4. countinghouse: the same. ✅
5. greenhouse, statehouse, mqttproxy: bump to v0.7.0 and `web/spec`. The AWS
   SDK and sqlite leave their module graphs. ✅

## Sequencing summary

1. Generalize boiler-sensor in place (in its repo). ✅ done
2. Lift the engine here as module `iot` (purely generic). ✅ done
3. Migrate `boiler-sensor` onto `common/iot`; golden bytes unchanged. ✅ done
4. Build `doorbell-sensor` (own repo) against `common/iot`. ✅ done, then parked
5. Validate consumers green, tag `iot/v0.1.0` and pin it. ✅ done
6. Publish this repo and the tag; ship boiler-sensor on the tag. ✅ done
7. Move `db`, `backup`, `web` (with `spec`) here from `identity/common`, then
   migrate identity, config, countinghouse, greenhouse, statehouse and
   mqttproxy. ✅ done
8. Config-service client module (next candidate 1). Separate trigger.
