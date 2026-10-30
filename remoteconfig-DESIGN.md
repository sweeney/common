# Design: `remoteconfig`, a shared client for config.swee.net

Status: **design, not built.** This is next candidate 1 in [PLAN.md](PLAN.md).
Written 2026-10-30 from a read of the four copies and of the config service's
own handlers.

## What exists today

| Consumer | File | Shape |
|---|---|---|
| statehouse | `internal/config/remote.go` (215) | `Fetcher.ApplyRemote` merges 3 namespaces into its `Config` at boot and on SIGHUP |
| countinghouse | `internal/config/remote.go` (578), `device.go` | Holds 3–4 namespaces as snapshots; `ResolveNamespaces` from `sites`; `Cold()` refuses boot |
| greenhouse | `internal/config/remote.go` (283), `device.go` | Holds devices + optional floorplan as snapshots |
| pi-price-screen | `internal/rulesconfig/rulesconfig.go` | `Client.Fetch` reads one namespace and merges it onto defaults |

These four are three-and-a-half copies of one fetcher. Each has the same
`TokenSource` interface, the same `NamespaceStatus` (`ok`, `fetched_at`,
`error` on `/healthz`), the same 10 s client and the same 1 MiB cap. pi-price-screen
is a separately written fourth: it cites countinghouse and has picked up the
fixes the others lack.

## What the config service actually promises

This is from `sweeney/config` `internal/handler/router.go` and
`internal/service/config_service.go`, which is the contract a client should be
written against:

| Answer | Meaning | What the client should do |
|---|---|---|
| `200` | Body is the document, **always a JSON object** (writes reject anything else). `X-Read-Role` / `X-Write-Role` headers | Decode it |
| `404` | Missing **or not readable by this caller**. This is deliberate and indistinguishable | One error that says both; never "doesn't exist" |
| `401` | Token bad or expired | Invalidate the token, retry **once** now |
| `403` | Token verified but its role is unrecognised. Not fixable by retrying | Permanent error |
| `400` | Name fails `^[a-z0-9_-]{1,64}$` | Never send it: validate locally first |
| `503` + `Retry-After` | Config cannot reach identity's JWKS. The token is **not** at fault | Keep last-good; do **not** invalidate the token |
| `429` + `Retry-After` | Rate limit, 5/s per IP with a burst of 20 | Keep last-good |

There is **no ETag, no version and no `If-None-Match`**: every read is
`Cache-Control: private, no-store`. The only change signal is `updated_at` per
namespace on `GET /api/v1/config` (the list). No consumer polls today; all
refresh at boot and on SIGHUP. So change detection is **out of scope**. If
polling ever arrives, add a strong `ETag` to config's GET (small and
server-side) rather than diff `updated_at` in the client.

Stored documents are capped at 128 KiB on write. A 1 MiB read cap is generous
headroom; pi-price-screen's 64 KiB is below what the service accepts and should
go.

## Where the copies diverge, and which should win

1. **401.** statehouse, countinghouse and greenhouse invalidate the token and
   *fail*. Because they refresh only at boot and on SIGHUP, the service then
   runs on last-good until someone sends another SIGHUP. pi-price-screen retries
   once immediately. **pi-price-screen wins.**
2. **503.** Every copy reports it as `unexpected status 503`. **New:** name it
   (identity unavailable at config) and carry `Retry-After`, so `/healthz`
   says what is actually wrong.
3. **Token-fetch failure.**
   - statehouse records no status at all.
   - greenhouse records it against devices but not floorplan.
   - countinghouse records it against `energy_tariffs` even when the
     agreements namespace is the one in use, which is a mislabel.

   **Fix by construction:** take the token per request, inside `Get`, so a token
   failure is simply that namespace's error.
4. **Empty `BaseURL`.**
   - greenhouse: a silent no-op.
   - countinghouse: a no-op, but it first logs the "not https" warning.
   - statehouse: guards at the call site in `main.go`.

   **greenhouse wins:** an empty URL is explicit local mode, it is not an
   error, and no warning is logged.
5. **Namespace in the error.** statehouse's error omits it. **Always include
   it.**
6. **Cold start.**
   - countinghouse latches "landed" per namespace and refuses to boot on a
     cold one.
   - greenhouse boots on an empty snapshot and serves confident zeros.
   - statehouse falls back to its local YAML, which is a real fallback.

   **countinghouse's latch moves into common** as an opt-in query. Whether
   greenhouse should adopt the refusal is a separate behaviour change; see
   open questions.
7. **Request hygiene.** Only pi-price-screen escapes the namespace
   (`url.PathEscape`), trims a trailing slash from the base URL, and sends
   `Accept` and `User-Agent`. **Adopt all four.** The `User-Agent` is
   `<service>/<version>`, which also makes config's logs say who is reading.
8. **Floorplan decoder (schema, not transport).** countinghouse refuses a `null`
   document and a wrapper shape with one collection that isn't an array.
   greenhouse silently skips such a collection. countinghouse's comment claims
   the two decoders accept the same set; they don't. **countinghouse wins**
   (loud rather than liberal).
9. **`DeviceConfig` fields.**
   - greenhouse lacks `Covers` (and `CoversWholeSite`).
   - Only greenhouse has `TargetTemperature` and `EnvironmentFields`.
   - The mirrors tag `Thresholds` fields `json:"-"`, so thresholds never
     decode outside statehouse.

   None of this breaks anything today, because unknown keys are ignored.

## Scope: what goes in, and what stays out

**In (phase 1, the module):** the transport and the bookkeeping that all four
share line for line:

- the HTTP GET;
- token, invalidate, retry once;
- status-code mapping;
- size cap and decode;
- per-namespace status;
- the landed/cold latch.

**Stays in each service:** what is done with a document.

- statehouse: `ApplyRemote`'s merge into `Config`, and the behaviour
  document's pointer fields.
- countinghouse:
  - `sites` resolution;
  - drift;
  - tariffs and agreements validation;
  - the choice of authoritative tariff namespace.
- greenhouse: the climate and ambient class tables and `MayReportField`.
- pi-price-screen: `Parse` and its notes.
- The snapshot holders (a mutex, a map and a copy-out accessor per
  collection). This is about 10 lines each, and the shapes differ: floors
  and rooms swap together. A generic holder would save little.

**Deferred (phase 2, a decision for later):** shared schema for the
`devices_<site>` and `floorplan_<site>` namespaces. The core device record:

- `Scheme`/`Primary`/`Display`;
- the legacy Z2M fields;
- `Class`, `DisplayName`, `Room`, `Floor`, `Covers`, `Location`,
  `EnergyStrategy`;
- `normaliseDevices`, `Place`, `CoversWholeSite`, `CoverageHouse`.

These are already identical in two services and nearly so in the third. The
floorplan decoder is shared by two, with a real divergence (item 8).
Consumers would embed the core and add their own fields. But the device record
is **statehouse's schema**, which it also loads from local YAML with its own
`Thresholds`. Putting it in common makes every schema change a common release.
The plan's rule says to extract what consumers show; they show the core but
not the owner's view of it. So land phase 1, then decide. Fix the floorplan
divergence in greenhouse now regardless; it is a bug either way.

## Module

`github.com/sweeney/common/remoteconfig`: its own `go.mod`, tags
`remoteconfig/vX.Y.Z`, `go 1.25.0` like its siblings. **Stdlib only** in
non-test code: no identity (callers pass any `TokenSource`, which
`identity/common/auth.TokenSource` already satisfies) and no yaml.

The name matches the `remote_config:` block the consumers already have in
their YAML. Not `config`: every consumer imports it from its own
`package config`, and it would sit one path segment from the
`github.com/sweeney/config` server.

## Public API

```go
package remoteconfig

// TokenSource is satisfied by identity/common/auth.TokenSource.
type TokenSource interface {
    Token(ctx context.Context) (string, error)
    Invalidate()
}

type Client struct {
    BaseURL    string       // empty: local mode, Get returns ErrNotConfigured without a token fetch
    Tokens     TokenSource
    UserAgent  string       // "<service>/<version>"; required
    HTTPClient *http.Client // nil: 10 s timeout
}

// Get fetches one namespace and decodes it into dst. The name is validated
// locally; a 401 invalidates the token and retries once.
func (c *Client) Get(ctx context.Context, ns string, dst any) error

// Errors. All wrap a *StatusError naming the namespace where one applies.
var (
    ErrNotConfigured // BaseURL empty
    ErrInvalidName   // fails ^[a-z0-9_-]{1,64}$; no request made
    ErrNotFound      // 404: missing or not readable by this caller
    ErrUnauthorized  // 401 after one retry with a fresh token
    ErrForbidden     // 403
    ErrUnavailable   // 503 (identity unreachable from config) or 429
)
type StatusError struct {
    Namespace  string
    Code       int
    RetryAfter time.Duration
}

// Status is the /healthz record. Its JSON is identical to today's, so no
// consumer's /healthz wire format changes.
type Status struct {
    OK        bool      `json:"ok"`
    FetchedAt time.Time `json:"fetched_at"`
    Error     string    `json:"error,omitempty"`
}

// Tracker records per-namespace outcomes. Safe for concurrent use.
type Tracker struct{ /* mu, statuses, landed */ }
func (t *Tracker) Record(ns string, err error)   // err == nil latches "landed" forever
func (t *Tracker) Statuses() map[string]Status    // copy
func (t *Tracker) Cold(namespaces ...string) []string // never landed, sorted
```

`Record` is the caller's job, after validation, and not part of `Get`. That
way a document which fetched fine but fails the service's own check (for
example countinghouse's tariffs) records a failure and never latches
"landed". This ordering is countinghouse's today, and it is the reason
`Get` does not record.

The calling pattern that replaces each `refreshX`:

```go
var doc floorplanDocument
err := f.client.Get(ctx, ns, &doc)
if err == nil { err = doc.validate() }
f.tracker.Record(ns, err)
if err != nil { f.warn(...); return } // keep last-good
swap(doc)
```

`Get` also refuses a body that is not a JSON object, so `null` can never count
as a successful fetch. config cannot store one, so this is defence only.

## Test plan

In `remoteconfig` (an `httptest` server, run with `-race`, aiming at 100%
coverage like `iot/logic`):

- **Each status code:** 200 decodes; 404/403/400 map to their sentinels; 503
  and 429 carry the parsed `Retry-After`.
- **401 handling:**
  - 401 then 200: the token is invalidated once, and the result is a
    success.
  - 401 twice: `ErrUnauthorized`, and no third request.
  - 503 does **not** invalidate the token.
- **Rejected before any request** (the server sees nothing):
  - an empty `BaseURL` (and no token is fetched);
  - an invalid or empty name.
- **Bodies:** oversize at 1 MiB + 1, `null`, an array, and invalid JSON are
  all errors.
- **Request shape:** `Authorization`, `Accept` and `User-Agent` are set,
  the namespace is escaped, and a trailing slash on the base URL is trimmed.
- **Context:** cancellation is honoured.
- **Tracker:**
  - the landed latch survives a later failure;
  - `Cold` is sorted;
  - `Statuses` returns a copy;
  - concurrent `Record`/`Statuses` is clean under `-race`;
  - the `Status` JSON is golden-pinned to today's bytes.

**Contract test, on the server side:** a test in `sweeney/config` drives
`remoteconfig` against config's real router, with an in-process JWKS. It pins
the answers the client relies on:

- 404 for unreadable, not 403;
- 503 + `Retry-After` when JWKS is down;
- 401 for an expired token;
- object-only documents.

config is the only place that can break this contract, so the test lives
where the change would be made. It also gives config its first real consumer
test.

**Per consumer:** existing fetcher tests keep passing against the new
internals; the 401-retry and per-namespace token-error changes get new tests;
`/healthz` is diffed before and after each deploy.

## Migration order

Each step is its own PR, ends green, and is deployed and smoke-tested before
the next starts.

0. **Prove the API before tagging.** Build `remoteconfig` on this branch.
   Port countinghouse (the most demanding consumer) and greenhouse against
   it locally, via a `go.work` outside `~/src/github.com/sweeney/` and never
   committed. Adjust the API, then tag `remoteconfig/v0.1.0`. This is the
   `iot` precedent: no tag until a real consumer has been built on it.
1. **config:** add the contract test (test-only dependency; no deploy needed).
2. **greenhouse:** the simplest snapshot consumer. Also fix the floorplan
   decoder (item 8).
3. **statehouse:** `ApplyRemote` keeps its merges; `fetch` and
   `recordStatus` go.
4. **countinghouse:** `landed`/`Cold` become `Tracker`; `sites` resolution
   stays. This fixes the agreements status mislabel.
5. **pi-price-screen:** `get`/`try` go and `Parse` stays. It deploys to a
   Pi whose `/healthz` and User-Agent carry the commit hash, so settle commit
   dates before deploying.

**Behaviour changes to name in each PR.** None is a pure move:

- 401 now retries immediately;
- token errors are recorded per namespace;
- `/healthz` error *text* changes (the shape does not);
- a `User-Agent` is sent.

Before each PR, check that no test or client matches on the old error strings.

## Open questions for the user

1. Should greenhouse refuse to boot on a cold devices namespace, as
   countinghouse does? Today it serves empty answers. This is a separate
   behaviour change, not part of the move.
2. Phase 2: should the shared device/floorplan schema be pursued after
   phase 1, or should statehouse stay the sole owner with the others
   mirroring?
