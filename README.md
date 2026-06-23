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
| `iot` | `github.com/sweeney/common/iot` | **planned (first to land)** | Paho MQTT, go-gpiocdev only |
| `auth` | `github.com/sweeney/common/auth` | planned (migrate from identity) | golang-jwt |
| `db` | `github.com/sweeney/common/db` | planned (migrate from identity) | modernc sqlite |
| `backup` | `github.com/sweeney/common/backup` | planned (migrate from identity) | AWS SDK (R2/S3) |
| … | | | (other identity/common packages, migrated lazily) |

See [`PLAN.md`](PLAN.md) for what lands when and why.

## Versioning

Each module is tagged independently with a path-prefixed semver tag:

```
iot/v0.1.0
auth/v1.0.0
```

Consumers pin an exact version (`go get github.com/sweeney/common/iot@v0.1.0`).
Never make a breaking change without bumping the module's minor (pre-1.0) or
major (post-1.0) version.

## Status

Empty scaffold. No code has landed yet — the first module (`iot`) arrives only
after the boiler-sensor generalization refactor is complete and validated by a
second consumer. See `PLAN.md`.
