# `github.com/sweeney/common/health`

The `/healthz` shared by Sweeney's Pi services, plus `health/sysinfo` for a
Pi's vital signs. Stdlib only.

## `health`

```go
mux.Handle("/healthz", health.Handler(func() health.Report {
	return health.Report{
		Status:  health.StatusOK, // or StatusStarting, StatusDegraded
		Detail:  "",              // omitted when empty
		Version: version,
		Start:   started,
		Fields:  []health.Field{{"ready", true}, {"system", sysinfo.Read(os.DirFS("/"), sysinfo.StatfsDisk("/"))}},
	}
}))
```

- **Always 200.** The `status` field carries the state, so a monitor reads
  one thing rather than guessing from a code.
- **Fixed order:** `status`, `detail`, `version`, `uptime_s`, `start_time`,
  `goroutines`, `heap_mb`, `sys_mb`, then the service's `Fields` in the order
  it gives them. A field named like a shared one is refused.
- Two-space indented, `Cache-Control: no-store`. `Now` and `Runtime` are
  filled in if left zero, and can be set for tests.

## `health/sysinfo`

`Read(fsys, disk)` reads, through an `fs.FS` rooted at `/`:
`sys/class/thermal/thermal_zone0/temp`, `proc/loadavg`, `proc/uptime`,
`proc/meminfo`, `run/pi-helper.env`, and the root disk through `StatfsDisk`.
A file that is missing or unparseable gives `null`, never 0. Only files seen
on the Pis in use are read. `PiHelperEnv` parses pi-helper's `NETWORK_*` file.

See [`health-DESIGN.md`](../health-DESIGN.md).
