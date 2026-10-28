# `github.com/sweeney/common/web`

Small, light-dependency helpers for HTTP services:

| Package | What it does |
|---|---|
| `apierr` | Shared sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrUserLimitReached`, `ErrTokenAlreadyRevoked`) for consistent error mapping. |
| `httputil` | Proxy-aware HTTP helpers: `ExtractClientIP` (honours `TRUST_PROXY`) and `CheckOrigin`. |
| `ratelimit` | Per-IP token-bucket rate-limiting middleware (`Limiter`), keyed by `httputil.ExtractClientIP`. Over-limit requests get a 429 with `Retry-After` set to the token refill interval in whole seconds, rounded up, never less than 1. |
| `spec` | OpenAPI YAML→JSON `Converter`, used to serve `/openapi.json` from embedded YAML. |

Moved here from `github.com/sweeney/identity/common` (`apierr`, `httputil`,
`ratelimit` in `web/v0.1.0`; `spec` in `web/v0.2.0`) at `common/v0.6.0`, with
their history. Pulls in `golang.org/x/time` and `gopkg.in/yaml.v3` only. Proxy and `TRUST_PROXY` configuration is documented in identity's
[`docs/deployment.md`](https://github.com/sweeney/identity/blob/master/docs/deployment.md).
