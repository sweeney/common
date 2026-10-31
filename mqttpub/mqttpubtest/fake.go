// Package mqttpubtest is a fake mqttpub.Publisher for tests.
package mqttpubtest

import (
	"fmt"

	"github.com/sweeney/common/mqttpub"
)

// Fake records every published message so tests can inspect them.  Shared
// across several daemon lifetimes it models the broker's retained store
// (see Retained).  It is not safe for concurrent use.
type Fake struct {
	Messages     []mqttpub.Message
	PublishError error
	// FailTopic, when set, selects which topics should fail.  Topics it does
	// not select are published normally.  This makes it possible to simulate a
	// partially degraded link — the case where one topic fails and everything
	// after it must still be attempted.  When nil, PublishError (if set)
	// applies to every topic.
	FailTopic func(topic string) bool
	// Failed records the topics whose publish returned an error, in order.
	Failed []string
	Closed bool
	// Disconnected makes Connected report false, for a health check.  It
	// does not fail publishes; set PublishError for that.
	Disconnected bool
}

// Publish appends the message to the recorded list, or returns an error if
// this topic is configured to fail.
func (f *Fake) Publish(msg mqttpub.Message) error {
	if err := f.errorFor(msg.Topic); err != nil {
		f.Failed = append(f.Failed, msg.Topic)
		return err
	}
	f.Messages = append(f.Messages, msg)
	return nil
}

// errorFor reports the error Publish should return for topic, or nil to let it
// through.
func (f *Fake) errorFor(topic string) error {
	if f.FailTopic == nil {
		return f.PublishError
	}
	if !f.FailTopic(topic) {
		return nil
	}
	if f.PublishError != nil {
		return f.PublishError
	}
	return fmt.Errorf("fake publish failure for %q", topic)
}

// Close marks the publisher as closed.
func (f *Fake) Close() error {
	f.Closed = true
	return nil
}

// Find returns the first message whose Topic matches, plus a found bool.
func (f *Fake) Find(topic string) (mqttpub.Message, bool) {
	for _, m := range f.Messages {
		if m.Topic == topic {
			return m, true
		}
	}
	return mqttpub.Message{}, false
}

// IndexOf returns the position of the first message with the given topic, or
// -1 if it was never published.  Tests use it to assert ordering — in
// ups-mqtt, that the outage message precedes the bulk telemetry.
func (f *Fake) IndexOf(topic string) int {
	for i, m := range f.Messages {
		if m.Topic == topic {
			return i
		}
	}
	return -1
}

// Retained returns the message a broker would deliver to a new subscriber of
// topic: the last retained message published to it, or false if there is
// none.  As on a real broker, a retained message with an empty payload
// deletes the retained message rather than replacing it.
//
// A single Fake shared across several daemon lifetimes therefore
// models the broker's memory, which outlives any one process.
func (f *Fake) Retained(topic string) (mqttpub.Message, bool) {
	var last mqttpub.Message
	found := false
	for _, m := range f.Messages {
		if m.Topic != topic || !m.Retained {
			continue
		}
		last, found = m, m.Payload != ""
	}
	return last, found
}

// Reset clears all recorded state so the fake can be reused between sub-tests.
func (f *Fake) Reset() {
	f.Messages = nil
	f.PublishError = nil
	f.FailTopic = nil
	f.Failed = nil
	f.Closed = false
	f.Disconnected = false
}

// Connected stands in for MQTTPublisher.Connected: true unless Disconnected.
func (f *Fake) Connected() bool { return !f.Disconnected }

// All returns the messages published to topic, in order.
func (f *Fake) All(topic string) []mqttpub.Message {
	var out []mqttpub.Message
	for _, m := range f.Messages {
		if m.Topic == topic {
			out = append(out, m)
		}
	}
	return out
}
