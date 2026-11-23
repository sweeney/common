# `github.com/sweeney/common/web`

Small, light-dependency helpers for HTTP services:

| Package | What it does |
|---|---|
| `apierr` | Shared sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrUserLimitReached`, `ErrTokenAlreadyRevoked`) for consistent error mapping. |
| `httputil` | Proxy-aware HTTP helpers: `ExtractClientIP` (honours `TRUST_PROXY`) and `CheckOrigin`. |
| `ratelimit` | Per-IP token-bucket rate-limiting middleware (`Limiter`), keyed by `httputil.ExtractClientIP`. Over-limit requests get a 429 with `Retry-After` set to the token refill interval in whole seconds, rounded up, from 1 to 241 (the idle-visitor cleanup horizon, after which any client gets a fresh bucket). |
| `spec` | OpenAPI YAML→JSON `Converter`, used to serve `/openapi.json` from embedded YAML. |
| `apiauth` | Bearer-token middleware for an API that people and services both call: a valid user token whose user may in (`UserOK`), or a valid service token whose client may in (`ServiceOK`; `AnyService` admits all, a refused client gets 403). Generic over the claim types, so `identity/common/auth.JWKSVerifier` fits as it is and this module doesn't depend on identity. A token that couldn't be checked (`KeysUnavailable`) is a 503 with `Retry-After`, never a 401, so an identity outage doesn't sign everyone out. Every policy is required. |
| `serve` | Around a read-only JSON API's handlers: `WriteJSON` (encoded first, so an unencodable value is a 500, not a broken 200) and `WriteError` (`{"error": msg}`), `CORS` (wildcard origin, Bearer tokens not cookies, preflights answered before auth), and `NewServer`/`Run` (the timeouts every service uses; serve until the context is done, then a graceful shutdown). Stdlib only. |

Moved here from `github.com/sweeney/identity/common` (`apierr`, `httputil`,
`ratelimit` in `web/v0.1.0`; `spec` in `web/v0.2.0`) at `common/v0.6.0`, with
their history. `apiauth` and `serve` (`web/v0.3.0`) come from the copies in
greenhouse, countinghouse and statehouse, for washhouse's API; moving
those services onto them is a follow-up. Pulls in `golang.org/x/time` and `gopkg.in/yaml.v3` only. Proxy and `TRUST_PROXY` configuration is documented in identity's
[`docs/deployment.md`](https://github.com/sweeney/identity/blob/master/docs/deployment.md).
