# Design: `health`, the shared `/healthz`

Status: **built locally, not tagged, not pushed** (2026-10-31).

## What exists today

| | boiler-sensor (`internal/web/healthz.go`) | pi-price-screen (`internal/httpapi/server.go`) |
|---|---|---|
| Codes | always 200 | always 200 |
| Statuses | `ok` / `starting` (no baseline) / `degraded` (MQTT down) | `ok` / `starting` (no frame) / `degraded` (`last_error`) |
| Body | struct, fixed order | `map`, so keys come out alphabetically |
| Shared fields | `status`, `detail`, `version`, `uptime_s`, `start_time`, `goroutines`, `heap_mb`, `sys_mb` | `status`, `detail`, `version`, `uptime_s`, `goroutines`, `heap_mb`, `sys_mb` |
| Own fields | `ready`, `mqtt_connected`, `channels` | `refreshes`, `gc_cycles`, `last_shown`, … |

Both copy `round1`, and boiler's comment says it follows pi-price-screen's.
tanktemp would be the third copy, and it adds a Pi system block that neither
has.

## Decisions

1. **One module, two packages:** `health` (the report and the handler) and
   `health/sysinfo` (the Pi's vital signs). Both stdlib only.
2. **The shared fields first, in boiler's order,** then the service's own
   fields in its order. `detail` is omitted when empty, as both do today.
3. **Keys and values stay the same for existing consumers, but the order can
   change.** Both deploy scripts read `version`/`status` by key. Each
   consumer writes a golden of today's body first and compares keys and
   values after moving.
4. **`sysinfo` reads only files seen on the Pis in use,** and gives `null`
   for anything missing.

## Consumers and tag

tanktemp first (through `go.work`); tag `health/v0.1.0` once it has run on a
Pi. Then boiler-sensor (sweeney/boiler-sensor#6, part 2) and pi-price-screen
(sweeney/pi-price-screen#3), each in its own session.
