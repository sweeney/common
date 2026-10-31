# `github.com/sweeney/common/mqttpub`

The MQTT publisher the fleet's daemons share: a `Publisher` interface, the real
paho-backed `MQTTPublisher` with its own capped-backoff reconnect, a fake for
tests, and real-broker test helpers. Extracted from ups-mqtt's
`internal/publisher`, `internal/retry` and `internal/mqtttest`; netmon carries a
copy. Paho is the only external dependency.

Topics and payloads belong to each service. The one shared shape is the
`{"online":false}` last will, offered as an optional helper.

**Status: built on a local branch, not tagged.** See
[`mqttpub-DESIGN.md`](../mqttpub-DESIGN.md).

## Packages

| Package | What it is |
|---|---|
| `mqttpub` | `Message`, `Publisher`, `Options`, `Tuning`, `MQTTPublisher`, `OfflinePayload` |
| `mqttpub/retry` | `Connect` with capped exponential backoff and sparing failure logs |
| `mqttpub/mqttpubtest` | `Fake`, a recording `Publisher` with error injection |
| `mqttpub/mqtttest` | real Mosquitto, a fault-injecting TCP proxy, a recorder (tests only) |

## API

| API | What it does |
|---|---|
| `Message{Topic, Payload, Retained, QOS}` | One publish. QoS is per message: telemetry at 0, events at 1 (see below). |
| `Publisher` | `Publish(Message) error`, `Close() error`. What a service's loop depends on. |
| `Options{Broker, ClientID, Username, Password, TLSCACert, WillQOS}` | Connection settings. Username/password are sent only when `Username` is set; `TLSCACert` is a PEM CA to trust. |
| `Tuning{KeepAlive, PingTimeout, PublishTimeout, Reconnect}`, `DefaultTuning()` | Timings: 30 s keep-alive, 10 s ping timeout, 5 s publish timeout, reconnect 1 s doubling to 30 s. Tests shrink them. |
| `NewMQTTPublisher(opts, lwtTopic, lwtPayload)`, `NewMQTTPublisherWith(..., Tuning)` | Builds the client without dialling. Fails only on what no retry fixes (an unreadable CA). The will is retained, at `WillQOS`. |
| `(*MQTTPublisher).Connect(ctx)` | One attempt; the caller retries it (`retry.Connect`). Returns at once when `ctx` ends. `ctx` of the first call also bounds the publisher's own reconnects. |
| `Publish(msg)` | Fails at once when the connection is not open, or while an earlier publish is unacknowledged past its timeout; otherwise waits up to `PublishTimeout` for the ack. |
| `Reconnected() <-chan struct{}` | One pending signal per re-established connection. Republish current state then: nothing was queued while down. |
| `Watch(topic, onMsg, onLost)` | A QoS 1 subscription made on every connect (call before `Connect`); `onLost` runs when the connection drops. |
| `Connected() bool` | Whether the connection is open now, for `/healthz`. |
| `Close()` | Clean DISCONNECT, unless the last publish failed: then the broker is left to fire the will in place of the lost offline announcement. |
| `OfflinePayload(at)` | `{"online":false,"timestamp":"…Z"}`; a zero `at` gives `{"online":false}`. |
| `retry.Connect(ctx, what, Backoff, connect, Wait)` | Retries until success or `ctx` ends. Logs the first failure, any changed error, and an unchanged one every 5 min. `retry.Sleep` is the production `Wait`. |
| `mqttpubtest.Fake` | Exported `Messages`, `PublishError`, `FailTopic`, `Failed`, `Closed`, `Disconnected`; `Find`, `IndexOf`, `All`, `Retained` (what a broker would hold, an empty retained payload deleting it), `Connected`, `Reset`. |
| `mqtttest` | `StartBroker`, `StartBrokerConf`, `StartBridged` (run a service's own mirrored bridge config with substitutions that must match), `StartProxy` with `Up`/`Blackhole`/`Down`, `Record`, `Retained`, `FreePort`, `ReadLines`. |

```go
pub, err := mqttpub.NewMQTTPublisher(mqttpub.Options{
    Broker: "tcp://broker.example:1883", ClientID: "svc", WillQOS: 1,
}, "svc/state", mqttpub.OfflinePayload(time.Time{}))
if err != nil {
    log.Fatal(err) // configuration, not connectivity
}
_, err = retry.Connect(ctx, "MQTT", retry.Backoff{Initial: time.Second, Max: time.Minute},
    func() error { return pub.Connect(ctx) }, retry.Sleep)
...
for {
    select {
    case <-tick:
        publishState(pub) // QOS 0 telemetry, QOS 1 events
    case <-pub.Reconnected():
        publishState(pub) // what was missed is made good now
    case <-ctx.Done():
        pub.Publish(mqttpub.Message{Topic: "svc/state", Retained: true, QOS: 1,
            Payload: mqttpub.OfflinePayload(time.Now())})
        pub.Close()
        return
    }
}
```

## Why paho's auto-reconnect is off

While auto-reconnecting, paho reports `IsConnected()` as true and holds every
QoS 1+ publish in its in-memory store, then resends the lot on reconnect in map
order. A live test of ups-mqtt saw 6½ hours of backlog, about 4,650 messages,
land on the broker in two seconds, shuffled, with a stale outage payload able
to arrive after its clear; every publish also waited out its timeout.

So `MQTTPublisher` reconnects itself on a clean session (which empties paho's
store), publishes only while `IsConnectionOpen()`, and signals `Reconnected()`
for the caller to republish current state. **Never turn auto-reconnect back
on.** Two guards bound a half-open link: after one publish times out, further
publishes fail at once until it is acked or the connection is replaced; a stall
past 2 × (keep-alive + ping timeout) replaces the connection, catching a broker
that answers pings but never acks.

## QoS per message kind

Behind a local broker bridged to a remote one, the bridge queues QoS 1
messages while its link is down, up to a cap, and drops the newest once full.
Telemetry at QoS 1 would fill that queue with readings the next poll
supersedes, and the event the queue exists for would be the one dropped. So
telemetry goes at QoS 0 and events (an outage, its clear, the offline
announcement, the will) at QoS 1. A new publish must pick its kind.

## Tests

```bash
cd mqttpub
go test -race ./...   # all tests
go vet ./...
GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 go build ./...
```

The `mqtttest` tests and `TestMQTTPublisher_AgainstBroker` need `mosquitto`
(`brew install mosquitto`, `apt install mosquitto`). Without it they skip,
except under CI (the `CI` variable set), where they fail.
