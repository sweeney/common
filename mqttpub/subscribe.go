package mqttpub

import (
	"errors"
	"fmt"
	"strings"
)

// Received is one message from a subscription.
type Received struct {
	Topic   string // the message's own topic, which a wildcard filter doesn't give
	Payload []byte // the handler's to keep
	// Retained is the broker's retain flag on delivery: true for a retained
	// message replayed because the subscription was (re)made, false for one
	// published while subscribed (MQTT 3.1.1 §3.3.1.3).
	Retained bool
}

// ValidFilter reports whether filter is a topic filter a broker accepts: not
// empty, no NUL, `+` only as a whole level, and `#` only as the whole last
// level.
func ValidFilter(filter string) error {
	if filter == "" {
		return errors.New("mqttpub: empty topic filter")
	}
	if strings.ContainsRune(filter, 0) {
		return fmt.Errorf("mqttpub: topic filter %q contains NUL", filter)
	}
	levels := strings.Split(filter, "/")
	for i, l := range levels {
		switch {
		case l == "#" && i != len(levels)-1:
			return fmt.Errorf("mqttpub: topic filter %q: # must be the last level", filter)
		case l != "#" && strings.Contains(l, "#"), l != "+" && strings.Contains(l, "+"):
			return fmt.Errorf("mqttpub: topic filter %q: a wildcard must be a whole level", filter)
		}
	}
	return nil
}

// Match reports whether topic matches filter, as a broker matches them. A
// filter starting with a wildcard doesn't match a topic starting with `$`
// (MQTT 3.1.1 §4.7.2).
func Match(filter, topic string) bool {
	if strings.HasPrefix(topic, "$") && (strings.HasPrefix(filter, "+") || strings.HasPrefix(filter, "#")) {
		return false
	}
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, l := range f {
		if l == "#" {
			return true // includes the parent level: a/# matches a
		}
		if i >= len(t) || (l != "+" && l != t[i]) {
			return false
		}
	}
	return len(f) == len(t)
}

// Subscriber is what a service's ingest depends on: MQTTPublisher, and
// mqttpubtest.Fake in tests.
type Subscriber interface {
	Subscribe(filter string, qos byte, onMsg func(Received), onLost func()) error
}

// CheckSubscription reports what Subscribe refuses: a bad filter, a QoS
// above 2, a nil handler. For fakes to refuse the same.
func CheckSubscription(filter string, qos byte, onMsg func(Received)) error {
	if err := ValidFilter(filter); err != nil {
		return err
	}
	if qos > 2 {
		return fmt.Errorf("mqttpub: QoS %d for %q: must be 0, 1 or 2", qos, filter)
	}
	if onMsg == nil {
		return fmt.Errorf("mqttpub: subscription to %q has no handler", filter)
	}
	return nil
}

// Subscribe adds a subscription to filter at qos, made on every connect:
// a clean session forgets subscriptions, and a retained message is only
// delivered on a fresh subscribe. onMsg gets each message (on paho's
// goroutine); onLost, if set, runs when the connection drops, since what
// is published while it is down is missed. Call it before Connect.
//
// MQTTPublisher is the one MQTT connection a service shares: its last will
// covers both directions, and publishing and subscribing reconnect
// together. Hence subscriptions live here, despite the name.
func (p *MQTTPublisher) Subscribe(filter string, qos byte, onMsg func(Received), onLost func()) error {
	if err := CheckSubscription(filter, qos, onMsg); err != nil {
		return err
	}
	p.mu.Lock()
	p.watches = append(p.watches, watch{topic: filter, qos: qos, onMsg: onMsg, onLost: onLost})
	p.mu.Unlock()
	return nil
}
