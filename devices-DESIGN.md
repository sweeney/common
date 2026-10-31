# Design: `devices`, the shared `devices_<site>` schema

Status: **design, not built.** This is phase 2 of next candidate 1 in
[PLAN.md](PLAN.md): the transport was shared as `remoteconfig` (phase 1); the
document it most often carries is still copied three times. Written 2026-10-31
from a read of statehouse `origin/master`, countinghouse and greenhouse
`origin/main`, and both live sites' `devices_<site>` namespaces.

## Decisions (2026-10-31)

1. **A top-level module, `github.com/sweeney/common/devices`**, not a
   sub-package of `remoteconfig`. The schema is not about transport: statehouse
   also reads devices from local YAML, and the `floorplan` tool builds the
   document offline. Neither needs an HTTP client to describe a device. Its own
   module also means a schema change doesn't bump the client's version, and vice
   versa. Tags: `devices/v0.1.0` onwards.
2. **This module owns the schema.** statehouse wrote the original, but once the
   type lives here it is a consumer like the others. A new key in
   `devices_<site>` is a PR here first.
3. **One schema for what the services read.** greenhouse's
   `target_temperature` and `environment_fields`, and statehouse's `thresholds`
   and `energy_strategy`, are facts about the device and already in the
   document. They are typed here, whichever service reads them. What a service
   *does* with a field stays in the service.

   **Except `coordinate` and `note`, on purpose** (revised 2026-10-31, while
   moving statehouse). A coordinate places someone's belongings to the
   centimetre in their home, and no service reads either key. statehouse
   deliberately has no field for them, so no later API change can leak them,
   and pins that in `TestNamespaceCoordinateAndNoteAreNotRelayed`. Typing them
   here would put them in every service. They decode as unknown keys; the
   floorplan tooling owns them.
4. **statehouse's coverage rule** is the shared one (below).
5. **A bad value loses only that field, never the document.** Today one
   malformed threshold makes statehouse drop the whole remote devices document.
   With one shared type that would spread to every service, and to every field.
   Instead, decoding is per field: a value that doesn't fit its type is dropped,
   reported, and the rest of the device kept.

## What exists today

| | statehouse | countinghouse | greenhouse |
|---|---|---|---|
| Type | `internal/config/config.go:377` | `internal/config/device.go:24` | `internal/config/device.go:24` |
| Fields | core + `thresholds`, `energy_strategy`, `covers` | core + `covers`, `energy_strategy` (thresholds untyped) | core + `target_temperature`, `environment_fields`, **no `covers`** |
| Thresholds | custom `UnmarshalJSON` (floats, duration strings) | `json:"-"`: ignored | `json:"-"`: ignored |
| Legacy fold (`normaliseDevices`) | yes | identical | identical |
| `Place()` | yes | identical | identical |
| Coverage | `Coverage()`: `covers`, else `house` if `location: house`, **even with `room` set** | `CoversWholeSite()`: false in that case | none |
| On a decode error | keeps local YAML devices only | keeps last-known snapshot | keeps last-known snapshot |

The core all three share: `scheme`, `primary`, `display`, `ieee_address`,
`friendly_name` (the last two are legacy Zigbee2MQTT shorthand),
`class`, `display_name`, `room`, `floor`, `location` (deprecated).

### What the live documents hold

The larger site's document, 48 devices: every one has `class`, `display_name`, `room`,
`floor` and `coordinate` (`[x, y]`, metres; not held, see decision 3). 41
use `ieee_address` and 7 use
`scheme`/`primary`; none use `display`. `environment_fields` 25,
`friendly_name` 17, `thresholds` 10, `energy_strategy` 8 (all `integration`),
`covers` 3 (all `house`), `note` 3, `target_temperature` 3. No `location`
remains.

The smaller site's, 2 devices: no `room`, `floor` or `coordinate`. An
absent room means unknown, not invalid.

The config service only checks that a document is a JSON object; nothing
validates devices.

## Module

`github.com/sweeney/common/devices`, standard library only. It does not import
`remoteconfig`: a service fetches the raw document with
`remoteconfig.Client.Get(ctx, ns, &raw)` (`raw` a `json.RawMessage`) and hands
it to `devices.Decode`. YAML tags are only strings, so statehouse can still load
local devices with yaml.v3 without this module importing it.

## Public API

```go
package devices

// CoverageHouse: the device's readings describe the whole site, not its room.
const CoverageHouse = "house"

// Device is one entry in a devices_<site> namespace, keyed by device id.
type Device struct {
	Scheme  string `yaml:"scheme"  json:"scheme,omitempty"`
	Primary string `yaml:"primary" json:"primary,omitempty"`
	Display string `yaml:"display" json:"display,omitempty"`

	IEEEAddress  string `yaml:"ieee_address"  json:"ieee_address,omitempty"`  // legacy
	FriendlyName string `yaml:"friendly_name" json:"friendly_name,omitempty"` // legacy

	Class       string `yaml:"class"        json:"class,omitempty"`
	DisplayName string `yaml:"display_name" json:"display_name,omitempty"`

	Room     string `yaml:"room"     json:"room,omitempty"`
	Floor    string `yaml:"floor"    json:"floor,omitempty"`
	Covers   string `yaml:"covers"   json:"covers,omitempty"`
	Location string `yaml:"location" json:"location,omitempty"` // deprecated
	// No coordinate or note: see decision 3.

	EnergyStrategy    string      `yaml:"energy_strategy"    json:"energy_strategy,omitempty"`
	Thresholds        *Thresholds `yaml:"thresholds"         json:"thresholds,omitempty"`
	TargetTemperature *float64    `yaml:"target_temperature" json:"target_temperature,omitempty"`
	EnvironmentFields []string    `yaml:"environment_fields" json:"environment_fields,omitempty"`
}

// Thresholds are per-device activity thresholds. Pointers, so an explicit
// zero is honoured. Durations are strings in JSON ("30s"), as today.
type Thresholds struct {
	IdleBelowW           *float64       `yaml:"idle_below_w"           json:"idle_below_w,omitempty"`
	ActiveAboveW         *float64       `yaml:"active_above_w"         json:"active_above_w,omitempty"`
	ActiveSustainedFor   *time.Duration `yaml:"active_sustained_for"   json:"active_sustained_for,omitempty"`
	InactiveSustainedFor *time.Duration `yaml:"inactive_sustained_for" json:"inactive_sustained_for,omitempty"`
	CompressorAboveW     *float64       `yaml:"compressor_above_w"     json:"compressor_above_w,omitempty"`
}

func (d Device) Place() string         // Room, else Location unless it is "house"
func (d Device) Coverage() string      // Covers, else CoverageHouse if Location is "house"
func (d Device) CoversWholeSite() bool // Coverage() == CoverageHouse
func (d *Device) Normalise()           // legacy fold, filling only empty fields

// Decode reads a devices_<site> document. It fails only if data is not a
// JSON object. A value that doesn't fit its field is dropped and reported;
// the rest of that device is kept. Every device comes back normalised.
func Decode(data []byte) (map[string]Device, []Problem, error)

// Check reports problems with a decoded document. It never refuses one.
func Check(m map[string]Device) []Problem

type Problem struct {
	ID      string // device id
	Field   string // JSON key, empty for whole-device problems
	Code    string // e.g. "device.bad_value", "device.floor_mismatch"
	Message string
}
```

Thresholds keep their custom `UnmarshalJSON` (moved from statehouse). That is
safe: it is on a named field's type, so it decodes only `thresholds`. `Device`
itself gets no decode method; `Decode` does the per-field work. Because nothing
is embedded, the promoted-method and yaml `,inline` traps don't arise.

Unknown keys are ignored, as today, so the namespace can gain a key before
this module does. Never use `DisallowUnknownFields`.

### Coverage

statehouse's rule: `covers` if set; else `house` if the legacy `location` is
`house`, even when `room` is set. statehouse pins that case
(`internal/device/profiles_coverage_test.go`, "room published first").
countinghouse currently says the opposite for it. No live device has `location`,
so nothing changes today.

### Check

Advisory only: each service shows the problems beside its namespace status on
`/healthz` and logs them when they change. Codes reuse the floorplan
validator's where they overlap:

| Code | When |
|---|---|
| `device.bad_value` | from `Decode`: a value didn't fit its field and was dropped |
| `device.floor_mismatch` | `room` isn't prefixed by `floor.` |
| `device.covers_unresolved` | `covers` is neither `house` nor room-shaped |
| `device.identity_missing` | no `scheme`/`primary` and no `ieee_address`: statehouse can never match it |
| `device.duplicate_identity` | two ids share `scheme:lower(primary)` or `scheme:lower(display)` (statehouse picks between them at random today) |
| `device.legacy_location` | still carries `location` |

A missing `room` or `floor` is not a problem (the smaller site has none). Turning
any of these into a refusal is a later, per-service decision.

## Behaviour changes, per service

| Service | Change |
|---|---|
| statehouse | A bad threshold loses that device's thresholds, not the whole remote document. |
| countinghouse | Coverage follows statehouse's rule (no live device affected). A bad value no longer costs the whole snapshot. |
| greenhouse | Gains `covers`, which it carries but doesn't act on (open question 2). A bad value no longer costs the whole snapshot. |

## Versioning

- Adding a field is a minor bump. Add the key to the namespace only after the
  services that read it have shipped; remove a key from the namespace before
  the field leaves the code (this is how `location` → `room` was done safely).
- No version marker inside the document. It is a bare `map[id]device`, so a
  top-level `"schema"` key would be read as a device.
- `Location` and the legacy Zigbee2MQTT fields stay until the namespaces have
  none and every service is on a release without them.

## Test plan

- Golden decode of made-up fixtures shaped like both live sites'
  documents: every field of every device, and zero problems.
- Per-field leniency: a string `target_temperature`, a bad duration in
  `thresholds`, an `environment_fields` that is a string. Each loses only
  that field, with one `device.bad_value`
  naming the id and key; the device's other fields survive.
- Not an object (array, string, `null`): an error.
- `coordinate` and `note` decode as unknown keys and never come back out.
- Coverage table, including statehouse's "room published first" case.
- `Normalise`: fold fills only empty fields; idempotent.
- `Check` on each code, and none on room-less devices.
- YAML: a statehouse-style local device with thresholds decodes with yaml.v3
  (in statehouse's tests, since this module doesn't import yaml.v3).

## Migration order

Each step is its own PR, deployed and verified before the next, as in phase 1.

0. **common:** build `devices` on a branch. Don't tag it yet: prove it on
   statehouse through a gitignored `go.work` first.
1. **statehouse**, the hardest case: local YAML devices, typed thresholds and
   the merge with remote. `DeviceConfig` becomes `devices.Device`; its
   `normaliseDevices`, `Place`, `Coverage` and `Thresholds` are deleted. Its
   tests must pass unchanged apart from the threshold-leniency change. Then tag
   `devices/v0.1.0`, deploy, and check that device ids and classifications on
   its API are the same before and after.
2. **countinghouse:** delete its copy and `CoversWholeSite`; add the coverage
   case to its tests. Compare `/devices` and a `/series` grouped by room before
   and after.
3. **greenhouse:** delete its copy; `MayReportField` and the catalog read the
   shared fields. Compare `/devices` before and after.
4. Later, separately: the floorplan document's schema, a shared `sites` type,
   and retiring `location`.

## Open questions for the user

1. **The `floorplan` repo** generated the larger site's devices document, but it has no git remote
   and its checked-in copy is 17 devices behind the live document, which is now
   edited in config directly. Retire its builder, or bring it back up to date
   and have it import this module? Either way it should be pushed somewhere.
2. Should greenhouse act on `covers`, e.g. leave `covers: house` devices out of
   room and floor averages? No climate device has `covers` today.
3. Should `floorplan-validate` and `Check` become one implementation?
4. Out of scope unless you say otherwise: where each service gets its
   `devices_namespace` name (local YAML in statehouse and greenhouse, the
   `sites` namespace in countinghouse).
