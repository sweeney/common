# `github.com/sweeney/common/web`

Small, stdlib-weight helpers for HTTP services:

| Package | What it does |
|---|---|
| `apierr` | Shared sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrUserLimitReached`, `ErrTokenAlreadyRevoked`) for consistent error mapping. |
| `httputil` | Proxy-aware HTTP helpers: `ExtractClientIP` (honours `TRUST_PROXY`) and `CheckOrigin`. |
| `ratelimit` | Per-IP token-bucket rate-limiting middleware (`Limiter`), keyed by `httputil.ExtractClientIP`. |

Moved here from `github.com/sweeney/identity/common` (`apierr`, `httputil`,
`ratelimit`) at `common/v0.6.0`, with their history. Pulls in `golang.org/x/time`
only. Proxy and `TRUST_PROXY` configuration is documented in identity's
[`docs/deployment.md`](https://github.com/sweeney/identity/blob/master/docs/deployment.md).
