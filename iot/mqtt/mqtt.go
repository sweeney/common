// Package mqtt provides MQTT publishing with abstraction for testing.
//
// It is sensor-agnostic: event topics, the system topic, the payload root key,
// and channel order all come from a profile.SensorProfile. The resilient
// RealPublisher reconnects with capped backoff, buffers events while offline,
// sets a last-will, and republishes a RECONNECTED notice on recovery.
package mqtt

import (
	"encoding/json"
	"time"

	"github.com/sweeney/common/iot/logic"
)

// EventPublisher publishes generic channel-keyed events plus system events.
type EventPublisher interface {
	// PublishEvent sends a generic channel-keyed event to the broker.
	PublishEvent(event logic.GenericEvent) error
	// PublishSystem sends a system lifecycle event to the broker.
	PublishSystem(event SystemEvent) error
	// Close disconnects from the broker.
	Close() error
}

// ConnectionStatus reports whether the MQTT connection is active.
type ConnectionStatus interface {
	IsConnected() bool
}

// SystemEvent represents a system lifecycle event (e.g., startup, shutdown, heartbeat).
type SystemEvent struct {
	Timestamp  time.Time
	Event      string // e.g., "STARTUP", "SHUTDOWN", "HEARTBEAT"
	Reason     string // e.g., "SIGTERM", "SIGINT" (shutdown only)
	Source     string // e.g., "last_will" (broker-published LWT); omitted for application-published events
	RawPayload []byte // Pre-formatted JSON payload; if set, FormatSystemPayload returns it directly
	Retained   bool   // Whether the message should be retained by the broker
}

// ChannelState represents a single channel's state in an event payload.
type ChannelState struct {
	State string `json:"state"`
}

// SystemPayload represents the MQTT message payload for system events.
// Used for simple events (LWT, RECONNECTED) that don't carry a full status snapshot.
type SystemPayload struct {
	System SystemPayloadInner `json:"system"`
}

// SystemPayloadInner contains the system event details.
type SystemPayloadInner struct {
	Timestamp string `json:"timestamp"`
	Event     string `json:"event"`
	Reason    string `json:"reason,omitempty"`
	Source    string `json:"source,omitempty"`
}

// FormatSystemPayload creates the JSON payload for a system event.
// If event.RawPayload is set, it is returned directly (used for full status snapshots).
func FormatSystemPayload(event SystemEvent) ([]byte, error) {
	if event.RawPayload != nil {
		return event.RawPayload, nil
	}

	payload := SystemPayload{
		System: SystemPayloadInner{
			Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
			Event:     event.Event,
			Reason:    event.Reason,
			Source:    event.Source,
		},
	}
	return json.Marshal(payload)
}
