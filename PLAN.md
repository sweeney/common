# Roadmap for `sweeney/common`

This repo exists to hold shared Go code as **independent modules**. It is
deliberately empty for now — code lands here only once it is proven generic by
more than one consumer. Premature extraction is how shared libraries get the
wrong API and then break every consumer fixing it.

## Module 1: `iot` (landed, tagged `iot/v0.1.0`)

> **Status: landed and tagged.** The generic engine lives in
> `github.com/sweeney/common/iot` as sub-packages `profile`, `logic`, `gpio`,
> `mqtt`, `status`, plus the `Run` daemon runtime in the root package — purely
> generic, with no boiler/CH/HW vocabulary. It builds and tests clean (`logic`
> and `status` at 100% coverage) and cross-compiles for `linux/arm`. A
> wire-contract self-test pins the legacy boiler bytes (2-channel boiler-shaped
> profile) plus a 1-channel doorbell smoke test. Both consumers — `boiler-sensor`
> and `doorbell-sensor` — are green against it, so `iot/v0.1.0` is cut and they
> pin the tag instead of a local `replace`.


**Source of the code:** the sensor engine currently in
`github.com/sweeney/boiler-sensor/internal/`. That repo is being generalized
from a hardwired two-channel heating daemon into a sensor-agnostic engine driven
by a per-binary profile (see `boiler-sensor/REFACTOR.md`).

**Lands only after** the boiler-sensor refactor is complete AND a second consumer
(a doorbell-press sensor on a separate Pi) has been built against the generalized
packages in-repo. Two real consumers validate the API before we commit to it as a
tagged module.

**Expected contents (the proven-generic engine):**

- `gpio` — N-channel `Reader` abstraction (real `go-gpiocdev` + fake), with raw
  active-low inversion handled in one place.
- `logic` — debounce / baseline / heartbeat detector, channel-agnostic
  (map-keyed channels, configurable event names).
- `mqtt` — the resilient publisher: reconnect with capped backoff, offline
  buffering, last-will, heartbeat enrichment. This is the crown jewel and the
  strongest reason the shared module exists.
- `status` — thread-safe status tracker plus ordered JSON encoders, rendering
  channels generically from a caller-supplied profile.
- `Run` (root package) — the daemon runtime: poll loop, signals, STARTUP,
  baseline → ready + scheduled HEARTBEAT, SHUTDOWN/LWT, tracker sync.

**Explicitly NOT in `iot`:** the per-binary `SensorProfile` (channel/pin/event
vocabulary, MQTT topics, payload root key, web title). Anything domain-specific —
"boiler/CH/HW", "doorbell/PRESS" — stays in each `cmd/*-sensor` binary.

Also not here: the **HTTP/HTML status page**. An earlier draft of this plan put a
`web` package in `iot`; it was pulled back out because the UI is service-specific
presentation, not engine. Each sensor repo owns its own `internal/web`, consuming
`iot/status` for the snapshot and JSON.

**Dependencies:** Paho MQTT + go-gpiocdev only. Targets 32-bit ARM (Pi Zero); no
CGO in test paths; keep the dependency graph minimal.

**First tag:** `iot/v0.1.0` (pre-1.0: API may still move between minor versions).

## Later: migrate `identity/common`

The existing shared packages in `github.com/sweeney/identity/common` (`auth`,
`apierr`, `backup`, `cli`, `db`, `httputil`, `ratelimit`, `secrets`, `spec`) will
move here as their own modules — split by dependency weight rather than ported as
one fat module.

**Trigger:** when a non-identity consumer needs them — e.g. when boiler-service
grows a server component that needs token auth (`auth`). Not before; today they
have only two tightly-related consumers (identity + config), so the split would be
cosmetic.

**Cost to plan for:** moving changes the import path
(`…/identity/common/*` → `…/common/*`), a breaking change for identity + config.
Batch it deliberately as a single coordinated migration; do not bundle it into the
boiler/doorbell work.

## Sequencing summary

1. Generalize boiler-sensor in place (in its repo). ✅ done
2. Lift the engine here as module `iot` (purely generic). ✅ done
3. Migrate `boiler-sensor` onto `common/iot` via `replace`; golden bytes unchanged. ✅ done
4. Build `doorbell-sensor` (own repo) against `common/iot` via `replace`. ✅ done
5. Validate both consumers green, then tag `iot/v0.1.0` and pin it. ✅ done
6. (Later, separate trigger) migrate `identity/common` packages here as modules.

**Open question:** whether the per-repo `internal/web` status page should move
here after all — the boiler and doorbell copies are currently byte-identical, so
the "service-specific presentation" rationale has produced no divergence. Decide
before a third sensor appears.
