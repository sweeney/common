package mqtt

import (
	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/profile"
)

// FakePublisher records published events for test assertions. It implements
// EventPublisher and ConnectionStatus.
type FakePublisher struct {
	// Events contains all generic events that were published.
	Events []logic.GenericEvent

	// Payloads contains the JSON payloads that were published.
	Payloads [][]byte

	// SystemEvents contains all system events that were published.
	SystemEvents []SystemEvent

	// SystemPayloads contains the JSON payloads for system events.
	SystemPayloads [][]byte

	// PublishError, if set, will be returned by PublishEvent.
	PublishError error

	// PublishSystemError, if set, will be returned by PublishSystem.
	PublishSystemError error

	// Closed tracks if Close was called.
	Closed bool

	// Connected controls the return value of IsConnected.
	Connected bool

	// Profile drives generic event serialization (PublishEvent).
	Profile profile.SensorProfile
}

// NewFakePublisher creates a FakePublisher for testing. Set Profile before
// publishing events so payloads serialize with the right root key/channels.
func NewFakePublisher() *FakePublisher {
	return &FakePublisher{}
}

// NewFakePublisherForProfile creates a FakePublisher bound to the given profile.
func NewFakePublisherForProfile(prof profile.SensorProfile) *FakePublisher {
	return &FakePublisher{Profile: prof}
}

// PublishEvent records a generic channel-keyed event, serializing it with the
// fake's profile.
func (f *FakePublisher) PublishEvent(event logic.GenericEvent) error {
	if f.PublishError != nil {
		return f.PublishError
	}

	f.Events = append(f.Events, event)
	payload := FormatEventPayload(f.Profile, event.Timestamp, event.Name, event.States)
	f.Payloads = append(f.Payloads, payload)
	return nil
}

// PublishSystem records the system event.
func (f *FakePublisher) PublishSystem(event SystemEvent) error {
	if f.PublishSystemError != nil {
		return f.PublishSystemError
	}

	f.SystemEvents = append(f.SystemEvents, event)

	payload, err := FormatSystemPayload(event)
	if err != nil {
		return err
	}
	f.SystemPayloads = append(f.SystemPayloads, payload)

	return nil
}

// Close marks the publisher as closed.
func (f *FakePublisher) Close() error {
	f.Closed = true
	return nil
}

// IsConnected reports whether the fake publisher is "connected".
func (f *FakePublisher) IsConnected() bool {
	return f.Connected
}

// Reset clears recorded events.
func (f *FakePublisher) Reset() {
	f.Events = nil
	f.Payloads = nil
	f.SystemEvents = nil
	f.SystemPayloads = nil
	f.Closed = false
	f.PublishError = nil
	f.PublishSystemError = nil
	f.Connected = false
}
