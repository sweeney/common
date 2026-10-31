# `github.com/sweeney/common/devices`

The schema of a `devices_<site>` namespace in the config service: one
`Device` per device id. This module owns the schema. A new key in the
namespace is a change here first, typed whichever service reads it; what a
service does with a field stays in the service. Stdlib only, and it does not
import `remoteconfig`: fetch the raw document with it, then decode it here.

| API | What it does |
|---|---|
| `Decode(data)` | Reads a document. It fails only if `data` is not a JSON object. A value that doesn't fit its field is dropped and reported (`device.bad_value`); the rest of that device is kept, so one typo never costs the whole document. Unknown keys are ignored. Devices come back normalised. |
| `Check(m)` | Advisory problems: `device.floor_mismatch`, `device.covers_unresolved`, `device.identity_missing`, `device.duplicate_identity`, `device.legacy_location`. Never refuses, never changes the map. A missing room or floor is unknown, not a problem. |
| `Device.Normalise()` | Folds the legacy Zigbee2MQTT `ieee_address`/`friendly_name` into `scheme`/`primary`/`display`, filling only empty fields. Call it after loading devices any other way, e.g. local YAML. |
| `Device.Place()` | The room it sits in: `room`, else the deprecated `location` unless that is `house`. |
| `Device.Coverage()`, `CoversWholeSite()` | What its readings describe: `covers`, else `house` if the legacy `location` says so, even when `room` is set. |

```go
var raw json.RawMessage
err := client.Get(ctx, ns, &raw) // remoteconfig
var m map[string]devices.Device
var problems []devices.Problem
if err == nil {
    m, problems, err = devices.Decode(raw)
}
tracker.Record(ns, err)
if err != nil {
    return // keep the last good snapshot
}
logIfChanged(problems, devices.Check(m)) // and show them on /healthz
swap(m)
```

`Device` carries yaml tags for services that also load devices from local
YAML; yaml.v3 reads the threshold durations (`"30s"`) natively. Never give
`Device` a JSON decode method, and never decode a document with
`DisallowUnknownFields`.

See [`devices-DESIGN.md`](../devices-DESIGN.md) for the design and the
migration plan.
