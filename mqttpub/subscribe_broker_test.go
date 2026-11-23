package mqttpub_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sweeney/common/mqttpub"
	"github.com/sweeney/common/mqttpub/mqtttest"
)

// Against a real Mosquitto: a wildcard subscription gets each message's
// topic; a retained message replayed by the subscribe carries the retain
// flag and a live one doesn't (even when it is published retained); and a
// reconnect replays the retained one again, flagged.
func TestSubscribe_AgainstBroker(t *testing.T) {
	broker := mqtttest.StartBroker(t, "")
	proxy := mqtttest.StartProxy(t, broker.Addr)
	p, err := mqttpub.NewMQTTPublisherWith(mqttpub.Options{
		Broker: proxy.URL(), ClientID: "mqttpub-sub-test", WillQOS: 1,
	}, "svc/status", mqttpub.OfflinePayload(time.Time{}), fastTuning)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []mqttpub.Received
	if err := p.Subscribe("in/+/state", 0, func(r mqttpub.Received) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	}, nil); err != nil {
		t.Fatal(err)
	}
	snap := func() []mqttpub.Received {
		mu.Lock()
		defer mu.Unlock()
		return append([]mqttpub.Received(nil), got...)
	}

	if err := publishVia(t, broker.URL(), "in/left/state", "old"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "the retained replay", func() bool { return len(snap()) == 1 })
	if r := snap()[0]; r.Topic != "in/left/state" || string(r.Payload) != "old" || !r.Retained {
		t.Errorf("replay %+v, want in/left/state old retained", r)
	}

	if err := publishVia(t, broker.URL(), "in/right/state", "live"); err != nil {
		t.Fatal(err)
	}
	if err := publishVia(t, broker.URL(), "in/right/other", "not matched"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "the live message", func() bool { return len(snap()) == 2 })
	if r := snap()[1]; r.Topic != "in/right/state" || string(r.Payload) != "live" || r.Retained {
		t.Errorf("live %+v, want in/right/state live, not retained", r)
	}

	proxy.SetMode(mqtttest.Down)
	waitFor(t, 5*time.Second, "the link down", func() bool { return !p.Connected() })
	proxy.SetMode(mqtttest.Up)
	select {
	case <-p.Reconnected():
	case <-time.After(10 * time.Second):
		t.Fatal("no reconnect")
	}
	waitFor(t, 5*time.Second, "both retained replayed", func() bool { return len(snap()) == 4 })
	for _, r := range snap()[2:] {
		if !r.Retained {
			t.Errorf("replay after reconnect %+v not flagged retained", r)
		}
	}
	p.Close() //nolint:errcheck
}
