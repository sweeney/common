package mqttpub_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sweeney/common/mqttpub"
	"github.com/sweeney/common/mqttpub/mqtttest"
	"github.com/sweeney/common/mqttpub/retry"
)

// fastTuning notices a dead link in seconds.
var fastTuning = mqttpub.Tuning{
	KeepAlive:      time.Second,
	PingTimeout:    time.Second,
	PublishTimeout: time.Second,
	Reconnect:      retry.Backoff{Initial: 50 * time.Millisecond, Max: 200 * time.Millisecond},
}

func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Against a real Mosquitto behind a link that drops: publish, Connected,
// the reconnect and its signal, and a Watch delivered again on reconnect.
func TestMQTTPublisher_AgainstBroker(t *testing.T) {
	broker := mqtttest.StartBroker(t, "")
	proxy := mqtttest.StartProxy(t, broker.Addr)
	rec := mqtttest.Record(t, broker.URL(), "svc/#")

	p, err := mqttpub.NewMQTTPublisherWith(mqttpub.Options{
		Broker: proxy.URL(), ClientID: "mqttpub-test", WillQOS: 1,
	}, "svc/state", mqttpub.OfflinePayload(time.Time{}), fastTuning)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var watched []string
	lost := 0
	p.Watch("svc/control", func(s string) {
		mu.Lock()
		watched = append(watched, s)
		mu.Unlock()
	}, func() {
		mu.Lock()
		lost++
		mu.Unlock()
	})
	seen := func() (int, int) { mu.Lock(); defer mu.Unlock(); return len(watched), lost }

	// Retained before connect: the watch's first subscribe delivers it.
	if err := publishVia(t, broker.URL(), "svc/control", "on"); err != nil {
		t.Fatal(err)
	}

	if p.Connected() {
		t.Error("Connected before Connect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !p.Connected() {
		t.Error("not Connected after Connect")
	}
	if err := p.Publish(mqttpub.Message{Topic: "svc/state", Payload: "up", Retained: true, QOS: 1}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, 5*time.Second, "the state on the broker", func() bool { return len(rec.On("svc/state")) == 1 })
	waitFor(t, 5*time.Second, "the watched retained message", func() bool { n, _ := seen(); return n == 1 })

	// The link drops: publishes fail at once, the watch is told, and the
	// broker fires the will.
	proxy.SetMode(mqtttest.Down)
	waitFor(t, 5*time.Second, "Connected to go false", func() bool { return !p.Connected() })
	if err := p.Publish(mqttpub.Message{Topic: "svc/state", Payload: "lost", QOS: 1}); err == nil {
		t.Error("Publish succeeded with the link down")
	}
	waitFor(t, 5*time.Second, "onLost", func() bool { _, l := seen(); return l == 1 })
	waitFor(t, 5*time.Second, "the will", func() bool {
		m := rec.On("svc/state")
		return len(m) == 2 && m[1].Payload == `{"online":false}`
	})

	// Back up: the publisher reconnects by itself and says so.
	proxy.SetMode(mqtttest.Up)
	select {
	case <-p.Reconnected():
	case <-time.After(10 * time.Second):
		t.Fatal("no Reconnected after the link came back")
	}
	if !p.Connected() {
		t.Error("not Connected after reconnecting")
	}
	waitFor(t, 5*time.Second, "the watch delivered again", func() bool { n, _ := seen(); return n == 2 })

	// The offline announcement got through, so Close disconnects cleanly
	// and the broker keeps it rather than firing the will.
	if err := p.Publish(mqttpub.Message{Topic: "svc/state", Payload: mqttpub.OfflinePayload(time.Now()), Retained: true, QOS: 1}); err != nil {
		t.Fatalf("Publish offline: %v", err)
	}
	p.Close() //nolint:errcheck
	rec.WaitQuiet(200*time.Millisecond, 3*time.Second)
	if m := rec.On("svc/state"); len(m) != 3 {
		t.Errorf("state messages %+v, want up, the will, the offline announcement", m)
	}
}

// publishVia publishes one retained QoS 1 message with a throwaway publisher.
func publishVia(t *testing.T, url, topic, payload string) error {
	t.Helper()
	p, err := mqttpub.NewMQTTPublisher(mqttpub.Options{Broker: url, ClientID: "mqttpub-seed"}, "svc/seed", "{}")
	if err != nil {
		return err
	}
	if err := p.Connect(context.Background()); err != nil {
		return err
	}
	defer p.Close() //nolint:errcheck
	return p.Publish(mqttpub.Message{Topic: topic, Payload: payload, Retained: true, QOS: 1})
}
