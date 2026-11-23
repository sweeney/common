package mqttpubtest

import "github.com/sweeney/common/mqttpub"

type subscription struct {
	filter string
	onMsg  func(mqttpub.Received)
	onLost func()
}

// Subscribe records a subscription, refusing what MQTTPublisher refuses.
func (f *Fake) Subscribe(filter string, qos byte, onMsg func(mqttpub.Received), onLost func()) error {
	if err := mqttpub.CheckSubscription(filter, qos, onMsg); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, subscription{filter, onMsg, onLost})
	return nil
}

// Deliver hands a message to every subscription whose filter matches it,
// in the order they were made, on the caller's goroutine, and reports how
// many got it. retained is the flag a broker would set: true for a replay
// on subscribe, false for a live message.
func (f *Fake) Deliver(topic string, payload []byte, retained bool) int {
	n := 0
	for _, s := range f.subsSnapshot() {
		if mqttpub.Match(s.filter, topic) {
			s.onMsg(mqttpub.Received{Topic: topic, Payload: append([]byte(nil), payload...), Retained: retained})
			n++
		}
	}
	return n
}

// Lose runs every subscription's onLost, as a dropped connection does.
func (f *Fake) Lose() {
	for _, s := range f.subsSnapshot() {
		if s.onLost != nil {
			s.onLost()
		}
	}
}

// Subscriptions lists the filters subscribed, in order.
func (f *Fake) Subscriptions() []string {
	var out []string
	for _, s := range f.subsSnapshot() {
		out = append(out, s.filter)
	}
	return out
}

func (f *Fake) subsSnapshot() []subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]subscription(nil), f.subs...)
}
