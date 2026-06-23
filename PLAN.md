# Roadmap for `sweeney/common`

This repo exists to hold shared Go code as **independent modules**. It is
deliberately empty for now — code lands here only once it is proven generic by
more than one consumer. Premature extraction is how shared libraries get the
wrong API and then break every consumer fixing it.

## Module 1: `iot` (first to land)

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
- `status` + `web` — thread-safe status tracker and HTTP/JSON status server,
  rendering channels generically from a caller-supplied profile.

**Explicitly NOT in `iot`:** the per-binary `SensorProfile` (channel/pin/event
vocabulary, MQTT topics, payload root key, web title). Anything domain-specific —
"boiler/CH/HW", "doorbell/PRESS" — stays in each `cmd/*-sensor` binary.

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

1. Generalize boiler-sensor in place (in its repo). ← in progress
2. Build doorbell-sensor against the generalized packages (in boiler-sensor repo).
3. Lift the engine here as module `iot`; tag `iot/v0.1.0`; both binaries import it.
4. (Later, separate trigger) migrate `identity/common` packages here as modules.
