// Package logic contains pure business logic for sensor state tracking.
// This package has NO external dependencies (no GPIO, MQTT, OS, or time.Sleep).
// Time is always injectable via time.Time parameters.
//
// The engine is sensor-agnostic: a Detector tracks N channels described by a
// profile.SensorProfile. Channels are identified by key; the event names a
// transition emits come from the profile, not from any hardcoded vocabulary.
package logic

import "time"

// State represents the logical state of a channel.
type State string

const (
	StateOn  State = "ON"
	StateOff State = "OFF"
)

// GenericEvent is a sensor-agnostic state transition. Key identifies the
// channel that changed; Name is the channel's configured On/Off event name;
// States is the full set of channel states keyed by channel key at the moment
// of the transition.
type GenericEvent struct {
	Timestamp time.Time
	Key       string
	Name      string
	States    map[string]State
}

// channelState tracks debounce state for a single channel.
type channelState struct {
	key string
	// Current stable (debounced) state
	stable State
	// Pending state during debounce
	pending State
	// Time when pending state was first observed
	pendingSince time.Time
	// Whether this channel has established a baseline
	baselined bool
	// Per-channel debounce duration (resolved from profile or default)
	debounce time.Duration
	// Event names emitted on transitions
	onEvent  string
	offEvent string
}

// HeartbeatData contains information for a heartbeat event. Counts are keyed by
// event name (the profile's On/Off event names).
type HeartbeatData struct {
	Timestamp time.Time
	Uptime    time.Duration
	Counts    map[string]int
}
