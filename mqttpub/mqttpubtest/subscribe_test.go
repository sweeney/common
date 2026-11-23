package mqttpubtest

import (
	"testing"

	"github.com/sweeney/common/mqttpub"
)

// Both the real publisher and the fake are Subscribers.
var (
	_ mqttpub.Subscriber = (*mqttpub.MQTTPublisher)(nil)
	_ mqttpub.Subscriber = (*Fake)(nil)
)

// Deliver hands a message to every subscription whose filter matches, as
// a broker would, and reports how many got it; Lose tells each
// subscription its connection dropped.
func TestFakeSubscribeDeliverLose(t *testing.T) {
	f := &Fake{}
	var states, all []mqttpub.Received
	lost := 0
	if err := f.Subscribe("in/+/state", 0, func(r mqttpub.Received) { states = append(states, r) }, func() { lost++ }); err != nil {
		t.Fatal(err)
	}
	if err := f.Subscribe("in/#", 1, func(r mqttpub.Received) { all = append(all, r) }, nil); err != nil {
		t.Fatal(err)
	}
	if n := f.Deliver("in/left/state", []byte("x"), true); n != 2 {
		t.Errorf("delivered to %d, want 2", n)
	}
	if n := f.Deliver("in/meter", []byte("y"), false); n != 1 {
		t.Errorf("delivered to %d, want 1", n)
	}
	if n := f.Deliver("out/a", []byte("z"), false); n != 0 {
		t.Errorf("delivered to %d, want 0", n)
	}
	if len(states) != 1 || states[0].Topic != "in/left/state" || string(states[0].Payload) != "x" || !states[0].Retained {
		t.Errorf("states %+v", states)
	}
	if len(all) != 2 || all[1].Topic != "in/meter" || all[1].Retained {
		t.Errorf("all %+v", all)
	}
	f.Lose()
	if lost != 1 {
		t.Errorf("lost %d, want 1 (the second has no onLost)", lost)
	}
	if got := f.Subscriptions(); len(got) != 2 || got[0] != "in/+/state" || got[1] != "in/#" {
		t.Errorf("subscriptions %v", got)
	}
}

// The fake refuses what the real one does.
func TestFakeSubscribeRefuses(t *testing.T) {
	f := &Fake{}
	if err := f.Subscribe("a/#/b", 0, func(mqttpub.Received) {}, nil); err == nil {
		t.Error("a bad filter accepted")
	}
	if err := f.Subscribe("a", 3, func(mqttpub.Received) {}, nil); err == nil {
		t.Error("QoS 3 accepted")
	}
	if err := f.Subscribe("a", 0, nil, nil); err == nil {
		t.Error("a nil handler accepted")
	}
	if len(f.Subscriptions()) != 0 {
		t.Error("a refused subscription was kept")
	}
}

// A handler may subscribe or deliver again without deadlocking.
func TestFakeDeliverReentrant(t *testing.T) {
	f := &Fake{}
	_ = f.Subscribe("a", 0, func(mqttpub.Received) {
		_ = f.Subscribe("b", 0, func(mqttpub.Received) {}, nil)
		f.Deliver("b", nil, false)
	}, nil)
	f.Deliver("a", nil, false)
}

// Subscribing and delivering from several goroutines is safe (run under
// -race).
func TestFakeSubscribeConcurrent(t *testing.T) {
	f := &Fake{}
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				_ = f.Subscribe("a/+", 0, func(mqttpub.Received) {}, func() {})
				f.Deliver("a/b", []byte("x"), false)
				f.Lose()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if n := len(f.Subscriptions()); n != 200 {
		t.Errorf("%d subscriptions, want 200", n)
	}
}
