# `github.com/sweeney/common/remoteconfig`

A client for the config service (config.swee.net): fetch a namespace, and
keep per-namespace status for `/healthz` and a cold-start check. Stdlib only.
It takes a `TokenSource` interface, which `identity/common/auth.TokenSource`
satisfies, so it does not depend on identity.

| Type | What it does |
|---|---|
| `Client` | `Get(ctx, ns, dst)`: one authenticated GET of `/api/v1/config/{ns}`, decoded into `dst`. It validates the name locally, retries a 401 once with a fresh token, and refuses a body that is not a JSON object or is over 1 MiB. An empty `BaseURL` is local mode (`ErrNotConfigured`, nothing requested); a nil `Tokens` sends anonymous requests, which can read only public namespaces. |
| `StatusError` | A non-200 answer: namespace, code, `Retry-After`, config's error message, and the sentinel it maps to. |
| `Tracker` | `Record(ns, err)`, `Statuses()`, `Cold(namespaces...)`. A success latches the namespace as landed for good, so a later failure is stale, not cold. Its `Status` JSON is the `{"ok","fetched_at","error"}` every consumer already serves. |

How config's answers map:

| Answer | Error | Token |
|---|---|---|
| 404 (missing **or** unreadable; config does not say which) | `ErrNotFound` | kept |
| 401 | retried once, then `ErrUnauthorized` | invalidated |
| 403 (role not recognised) | `ErrForbidden` | kept |
| 503 (config cannot reach identity), 429 (rate limit) | `ErrUnavailable`, with `RetryAfter` | kept |
| anything else non-200 | `ErrUnexpectedStatus` | kept |

`Get` records nothing. Call `Tracker.Record` after your own validation, so a
document that fetched but was refused never counts as landed:

```go
var doc tariffDoc
err := client.Get(ctx, ns, &doc)
if err == nil {
    err = doc.Validate()
}
tracker.Record(ns, err)
if err != nil {
    return // keep the last good document
}
swap(doc)
```

Holding the last good document, and what to do with it, stays in each
service. See [`remoteconfig-DESIGN.md`](../remoteconfig-DESIGN.md) for the
design and the migration plan.
