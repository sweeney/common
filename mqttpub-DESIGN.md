# Design: `mqttpub`, the shared MQTT publisher

Status: **built locally, not tagged, not pushed.** This is next candidate 2 in
[PLAN.md](PLAN.md). Written 2026-10-31 from a read of ups-mqtt (`c4bd506`) and
netmon (`7d9b91f`), whose publishers had stopped diverging: netmon's is a
declared copy of ups-mqtt's with two additions. The trigger is tanktemp, a
third service (DS18B20 readings) that would otherwise make a third copy.

## Decisions (2026-10-31)

1. **A top-level module, `github.com/sweeney/common/mqttpub`**, Paho as the
   only external dependency, pinned to ups-mqtt's `v1.4.3`. `go 1.23`, the
   directive ups-mqtt and netmon both have, so neither has to raise its own
   to adopt it. It is not `iot/mqtt`: that publisher is bound to the sensor
   profile and payload shape and has an offline ring buffer, which these
   services deliberately don't (point 3).
2. **ups-mqtt is the source of truth.** Its code moved with its tests, the
   tests first. Where netmon's copy had moved on (its `Options`, `Watch`, the
   wedge test), netmon's version was taken.
3. **Behaviour is unchanged.** Paho auto-reconnect stays off; the publisher's
   own capped-backoff reconnect on a clean session, the publish timeout, the
   stall and wedge guards, the skipped clean DISCONNECT after a failed last
   publish and the `Reconnected` signal are ups-mqtt's, line for line.
   Nothing is queued while down: the caller republishes current state on
   `Reconnected`, which is what a subscriber wants.
4. **No service's config package is imported.** `Options` is netmon's struct
   (broker, client ID, username, password, CA file, will QoS). Timings
   (keep-alive, ping timeout, publish timeout, reconnect backoff) stay in
   `Tuning`, not `Options`, as both services have them.
5. **QoS is per message**, and the will's QoS is given (`WillQOS`). ups-mqtt
   derived it from its own config (`EffectiveEventQOS()`); it now passes
   that value.
6. **No topics or payload shapes, bar one.** `OfflinePayload` builds the
   `{"online":false}` will that ups-mqtt, netmon and tempest-mqtt use, as an
   optional helper. Everything else (state, outage, its clear) stays in the
   service.
7. **`Connected()` is new.** A `/healthz` needs it. It reports
   `IsConnectionOpen()`, the test `Publish` makes, never `IsConnected()`,
   which paho keeps true while auto-reconnecting.
8. **One fake, `mqttpubtest.Fake`.** ups-mqtt's `FakePublisher` (exported
   fields, `Find`, `IndexOf`, `Retained`, `Reset`, `PublishError`,
   `FailTopic`/`Failed`) plus netmon's `All` and a `Disconnected` toggle for
   `Connected`. The package is `mqttpubtest`, after `httptest`.
9. **`mqtttest` keeps no service's broker config.** ups-mqtt embedded a
   mirror of its host's local-broker and bridge config; `StartBridged` now
   takes the caller's mirror, listen address and substitutions. A
   substitution that no longer matches still fails the test.

## What exists today

| | ups-mqtt `internal/publisher` | netmon `internal/publisher` |
|---|---|---|
| Origin | the original | "Adapted from ups-mqtt … reconnect and stall handling are unchanged" |
| Connection settings | `config.MQTTConfig` (TOML struct; will QoS via `EffectiveEventQOS()`) | `Options{Broker, ClientID, Username, Password, TLSCACert, WillQOS}` |
| `MQTTPublisher` | Connect, Publish, Close, Reconnected, stall + wedge guards (wedge at 2 × (keep-alive + ping timeout)) | identical, plus `Watch(topic, onMsg, onLost)`, resubscribed by an OnConnect handler, with `onLost` from `connectionLost` and the wedge guard |
| Fake | `FakePublisher`: `Messages`, `PublishError`, `FailTopic`, `Failed`, `Closed`; `Find`, `IndexOf`, `Retained`, `Reset` | `Fake`: `Messages`, `Fail bool`, `Failed`, `Closed`; `Retained`, `All` |
| `Message.QOS` | per message | per message |
| Service code in the package | state/outage/last_outage payloads, `PublishAll`, `FormatOffline` | none (payloads in `internal/payload`) |
| `retry` | `Backoff`, `Connect`, `Sleep`, `Wait`, `Result` | identical code and tests; only the package comment differs |
| Fake `paho.Client` in tests | Subscribe is a no-op | records subscribes, can fail them |
| Tests only in this one | — | `watch_test.go`, `wedge_test.go` (wedge is 2×) |
| `mqtttest` | Mosquitto, proxy, recorder, embedded bridge mirror | none |
| Offline payload | `{"online":false,"timestamp":"…Z"}` (UTC, seconds) for will and shutdown | will `{"online":false}`; shutdown adds a `time.Time` timestamp (RFC 3339 with nanoseconds) |

tempest-mqtt has its own, older publisher and a bare `{"online":false}` will.
It is not part of this work.

### How each difference was resolved

- **`config.MQTTConfig` vs `Options`:** `Options`. ups-mqtt passes
  `WillQOS: cfg.EffectiveEventQOS()`.
- **`Watch`:** taken from netmon unchanged, with its tests.
- **Fake:** ups-mqtt's, renamed `Fake`, plus `All`. netmon's `Fail: true`
  becomes `PublishError: errors.New(…)`: the same thing, one way to say it.
  ups-mqtt's `TestFakePublisher_PublishError` drove its `PublishAll`; it now
  calls `Publish` directly.
- **Tests' fake `paho.Client`:** netmon's, the superset.
- **Offline timestamp:** ups-mqtt's format. `OfflinePayload(time.Time{})` is
  the bare shape netmon and tempest-mqtt send as their will.
  `OfflinePayload(time.Now())` is byte-for-byte ups-mqtt's `FormatOffline()`.
  netmon's shutdown message carries nanoseconds and would lose them if it
  switched, so it keeps its own payload unless that is decided otherwise.
- **`StartBridged`:** made generic (decision 9). `BridgeNotificationTopic`
  and the `bridge/` mirror stay in ups-mqtt, with a short wrapper there.

## Public API

```go
package mqttpub // github.com/sweeney/common/mqttpub

type Message struct {
    Topic    string
    Payload  string
    Retained bool
    QOS      byte
}
type Publisher interface {
    Publish(msg Message) error
    Close() error
}
type Options struct {
    Broker, ClientID, Username, Password, TLSCACert string
    WillQOS                                          byte
}
type Tuning struct {
    KeepAlive, PingTimeout, PublishTimeout time.Duration
    Reconnect                              retry.Backoff
}
const DefaultPublishTimeout = 5 * time.Second
func DefaultTuning() Tuning
func NewMQTTPublisher(cfg Options, lwtTopic, lwtPayload string) (*MQTTPublisher, error)
func NewMQTTPublisherWith(cfg Options, lwtTopic, lwtPayload string, tn Tuning) (*MQTTPublisher, error)
func (p *MQTTPublisher) Connect(ctx context.Context) error
func (p *MQTTPublisher) Publish(msg Message) error
func (p *MQTTPublisher) Close() error
func (p *MQTTPublisher) Reconnected() <-chan struct{}
func (p *MQTTPublisher) Connected() bool
func (p *MQTTPublisher) Watch(topic string, onMsg func(payload string), onLost func())
func OfflinePayload(at time.Time) string

package retry // github.com/sweeney/common/mqttpub/retry

type Backoff struct{ Initial, Max time.Duration }
type Wait func(ctx context.Context, d time.Duration) error
type Result struct{ Attempts int; Waited time.Duration }
const RepeatLogEvery = 5 * time.Minute
func Connect(ctx context.Context, what string, b Backoff, connect func() error, wait Wait) (Result, error)
func Sleep(ctx context.Context, d time.Duration) error

package mqttpubtest // github.com/sweeney/common/mqttpub/mqttpubtest

type Fake struct {
    Messages     []mqttpub.Message
    PublishError error
    FailTopic    func(topic string) bool
    Failed       []string
    Closed       bool
    Disconnected bool
}
// Publish, Close, Find, IndexOf, Retained, All, Connected, Reset

package mqtttest // github.com/sweeney/common/mqttpub/mqtttest

func MosquittoPath(t testing.TB) string
func StartBroker(t testing.TB, extra string) *Broker
func StartBrokerConf(t testing.TB, conf, addr string) *Broker
type Substitution struct{ From, To string }
func StartBridged(t testing.TB, conf, addr string, subs ...Substitution) *Broker
// Broker{Addr, Conf}: Stop, Restart, Logs, URL
func FreePort(t testing.TB) int
type Mode int // Up, Blackhole, Down
func StartProxy(t testing.TB, target string) *Proxy
// Proxy{Addr}: URL, SetMode, Close
func Record(t testing.TB, brokerURL, filter string) *Recorder
// Recorder: Messages, WaitQuiet, On
type Received struct{ At time.Time; Topic, Payload string; Retained bool }
func Retained(t testing.TB, brokerURL, topic string) (string, bool)
func ReadLines(path string) ([]string, error)
```

`Message.QOS` keeps the spelling both services use, so their call sites and
the moved tests are unchanged.

## Deliberately left out

- **An offline queue or ring buffer.** A stale backlog is what auto-reconnect
  delivered, shuffled. Republish on `Reconnected` instead.
- **Paho auto-reconnect, as an option.** No knob to turn it back on.
- **Topic builders and payload types** (state, outage, last_outage, the
  timestamp format of anything but `OfflinePayload`).
- **A `Connected` method on `Publisher`.** A service that needs it for
  `/healthz` takes a narrower interface of its own; the loop needs only
  `Publish` and `Close`.
- **Locking in the fake.** Neither service's fake had it; their tests publish
  from one goroutine.
- **Subscribe QoS or a multi-topic `Watch`.** netmon's one use is a retained
  bridge status at QoS 1.
- **tempest-mqtt.** Its publisher predates this lineage; moving it is a
  separate decision.

## Test plan (as built)

- Moved unchanged apart from package and import paths: ups-mqtt's `retry`
  tests; netmon's `real_test`, `timeout_test`, `reconnect_test`,
  `wedge_test`, `watch_test` (ups-mqtt's own, with `Options` in place of
  `config.MQTTConfig`, plus the wedge and watch tests); ups-mqtt's proxy test.
- ups-mqtt's fake tests (`Find`, `Retained`, `PublishError`, `Close`,
  `Reset`), plus new ones for `FailTopic`, `IndexOf`, `All`, `Connected` and
  `Reset` clearing the injection. `mqttpubtest` is at 100% coverage.
- New: `Connected` against open, closed and auto-reconnecting clients;
  `OfflinePayload` bytes, stamped and bare.
- New, against a real Mosquitto: the broker, `Restart`, recorder and
  `Retained`; `StartBridged` forwarding to an uplink and failing on a stale
  substitution; and `MQTTPublisher` through the proxy (connect, publish,
  watch, dropped link, will, reconnect, re-watch, clean close).
- CI installs Mosquitto; without it the broker tests skip locally and fail
  under CI.

## Migration order

Each step is its own PR in the consumer's repo, deployed and verified before
the next. None is done yet.

0. **common:** build `mqttpub` on a branch (done locally, not pushed). Don't
   tag it: prove it on ups-mqtt through a gitignored `go.work` first.
1. **ups-mqtt**, the original. `internal/publisher` keeps only its payloads,
   `PublishAll` and friends against `mqttpub.Publisher`; `real.go`, `fake.go`,
   `internal/retry` and `internal/mqtttest` (bar the `bridge/` mirror, the
   notification topic and a `StartBridged` wrapper) are deleted.
   `FakePublisher` becomes `mqttpubtest.Fake`; `FormatOffline()` becomes
   `mqttpub.OfflinePayload(time.Now())`. Its tests, including the live and
   bridged replays, must pass unchanged apart from names. Deploy and verify
   (state, outage clear and will on the broker, a forced reconnect) before
   tagging `mqttpub/v0.1.0`.
2. **netmon:** delete its copy; `Fake{Fail: true}` becomes
   `Fake{PublishError: …}`. Its own offline payload stays. Deploy and check
   the bridge watch still reports.
3. **tanktemp**, the new DS18B20 service, written against the tag from the
   start, with `Connected()` on its `/healthz`.

## Versioning

- Tag `mqttpub/v0.1.0` only after ups-mqtt has run on the untagged module in
  production. Until then consumers use a `go.work`.
- Any change to reconnect, stall or close behaviour is at least a minor bump
  and is proven on ups-mqtt's live and bridged replays first.
- Adding a method or an `Options` field is a minor bump; removing or renaming
  one before 1.0 is too, and is called out in the tag message.

## Open questions for the user

1. Should netmon adopt `OfflinePayload(time.Now())` for its shutdown message,
   giving up nanoseconds in the timestamp, so all three services send one
   shape?
2. `Message.QOS` keeps the services' spelling rather than Go's `QoS`. Rename
   before `v0.1.0` if you prefer; it touches every call site in both services.
