package mqttpub

import (
	"testing"
	"time"
)

// incoming is a message paho hands a handler, with its retain flag.
type incoming struct {
	topic, payload string
	retained       bool
}

func (m incoming) Duplicate() bool   { return false }
func (m incoming) Qos() byte         { return 0 }
func (m incoming) Retained() bool    { return m.retained }
func (m incoming) Topic() string     { return m.topic }
func (m incoming) MessageID() uint16 { return 1 }
func (m incoming) Payload() []byte   { return []byte(m.payload) }
func (m incoming) Ack()              {}

// A subscription is made on every connect at its own QoS, and hands its
// handler each message's topic, payload and retain flag: a consumer of a
// wildcard needs the topic, and the flag tells a broker's replay of a
// retained message from a live one.
func TestSubscribe_TopicPayloadRetained(t *testing.T) {
	c := &fakeClient{connected: true}
	var waits []time.Duration
	p := newTestPublisher(c, &waits)
	var got []Received
	if err := p.Subscribe("water/hot/tanktemp/+/state", 0, func(r Received) { got = append(got, r) }, nil); err != nil {
		t.Fatal(err)
	}
	p.subscribe(c)
	if len(c.subscribed) != 1 || c.subscribed[0].topic != "water/hot/tanktemp/+/state" || c.subscribed[0].qos != 0 {
		t.Fatalf("subscribed %+v", c.subscribed)
	}
	h := c.subscribed[0].handler
	h(c, incoming{"water/hot/tanktemp/left/state", `{"a":1}`, true})
	h(c, incoming{"water/hot/tanktemp/right/state", `{"b":2}`, false})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	want := []struct {
		topic, payload string
		retained       bool
	}{{"water/hot/tanktemp/left/state", `{"a":1}`, true}, {"water/hot/tanktemp/right/state", `{"b":2}`, false}}
	for i, w := range want {
		if got[i].Topic != w.topic || string(got[i].Payload) != w.payload || got[i].Retained != w.retained {
			t.Errorf("message %d: %+v, want %+v", i, got[i], w)
		}
	}
	p.subscribe(c) // a reconnect subscribes again
	if len(c.subscribed) != 2 {
		t.Errorf("%d subscribes after a reconnect, want 2", len(c.subscribed))
	}
}

// The handler owns its payload: paho may reuse the buffer.
func TestSubscribe_PayloadIsACopy(t *testing.T) {
	c := &fakeClient{connected: true}
	var waits []time.Duration
	p := newTestPublisher(c, &waits)
	var got Received
	if err := p.Subscribe("a/b", 1, func(r Received) { got = r }, nil); err != nil {
		t.Fatal(err)
	}
	p.subscribe(c)
	m := &mutable{incoming: incoming{topic: "a/b"}, buf: []byte("xyz")}
	c.subscribed[0].handler(c, m)
	m.buf[0] = 'Q'
	if string(got.Payload) != "xyz" {
		t.Errorf("payload %q changed with paho's buffer", got.Payload)
	}
}

type mutable struct {
	incoming
	buf []byte
}

func (m *mutable) Payload() []byte { return m.buf }

// Watch is a QoS 1 Subscribe that hands over the payload alone.
func TestWatch_IsASubscribe(t *testing.T) {
	c := &fakeClient{connected: true}
	var waits []time.Duration
	p := newTestPublisher(c, &waits)
	var got string
	p.Watch("x/y", func(s string) { got = s }, nil)
	p.subscribe(c)
	c.subscribed[0].handler(c, incoming{"x/y", "on", false})
	if got != "on" || c.subscribed[0].qos != 1 {
		t.Errorf("got %q at QoS %d", got, c.subscribed[0].qos)
	}
}

// Subscribe refuses what the broker would: a bad filter or QoS, and a nil
// handler.
func TestSubscribe_Refuses(t *testing.T) {
	c := &fakeClient{connected: true}
	var waits []time.Duration
	p := newTestPublisher(c, &waits)
	h := func(Received) {}
	for _, tc := range []struct {
		filter string
		qos    byte
		h      func(Received)
	}{
		{"", 0, h}, {"a/#/b", 0, h}, {"a/b#", 0, h}, {"a/+b", 0, h}, {"a/b", 3, h}, {"a/b", 0, nil}, {"a/\x00", 0, h},
	} {
		if err := p.Subscribe(tc.filter, tc.qos, tc.h, nil); err == nil {
			t.Errorf("Subscribe(%q, %d, nil handler %v) accepted", tc.filter, tc.qos, tc.h == nil)
		}
	}
	p.subscribe(c)
	if len(c.subscribed) != 0 {
		t.Errorf("a refused subscription was made: %+v", c.subscribed)
	}
}

func TestValidFilterAndMatch(t *testing.T) {
	for _, f := range []string{"#", "+", "a", "a/b", "a/+/c", "a/#", "+/+", "/a", "a/", "a//b", "+/#"} {
		if err := ValidFilter(f); err != nil {
			t.Errorf("ValidFilter(%q): %v", f, err)
		}
	}
	for _, tc := range []struct {
		filter, topic string
		want          bool
	}{
		{"a/b", "a/b", true}, {"a/b", "a/c", false}, {"a/b", "a/b/c", false},
		{"a/+/c", "a/x/c", true}, {"a/+/c", "a/x/y/c", false}, {"a/+", "a/", true}, {"a/+", "a", false},
		{"a/#", "a", true}, {"a/#", "a/b/c", true}, {"#", "a/b", true}, {"+/#", "a", true},
		{"water/hot/tanktemp/+/state", "water/hot/tanktemp/meter", false},
		{"#", "$SYS/broker", false}, {"+/broker", "$SYS/broker", false}, {"$SYS/#", "$SYS/broker", true},
	} {
		if got := Match(tc.filter, tc.topic); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}
