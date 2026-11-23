// Package boiler decodes boiler-sensor's MQTT messages: the programmer's
// switch events (`energy/boiler/sensor/events`) and its lifecycle messages
// (`energy/boiler/sensor/system`, retained). boiler-sensor's README and its
// wire-contract test are the source; there is no WIRE.md.
//
// The system topic carries two shapes:
//   - `{"status":{...}}`: STARTUP, HEARTBEAT and SHUTDOWN, with a full
//     snapshot including the HW and CH states;
//   - `{"system":{...}}`: compact events such as RECONNECTED, and the last
//     will (SHUTDOWN, reason MQTT_DISCONNECT, source last_will), with no
//     switch states.
//
// After a reconnect the retained system message can be RECONNECTED rather
// than a heartbeat, so a consumer must never assume the retained message
// carries HW/CH: a message without them decodes with both Unknown.
//
// A message that fails to decode is an error, never a zero value. Unknown
// keys are ignored (boiler-sensor adds fields, such as network, over time).
//
// It was written for washhouse (its first consumer) and moved here so
// statehouse's adapters can share it.
package boiler

import "time"

// Switch is a programmer channel's state: on, off, or unknown.
type Switch uint8

// Switch states. The zero value is Unknown, so a field that wasn't on the
// wire reads as Unknown, never Off.
const (
	Unknown Switch = iota
	Off
	On
)

func (s Switch) String() string {
	switch s {
	case Off:
		return "off"
	case On:
		return "on"
	}
	return "unknown"
}

// Switch events on the events topic.
const (
	EventCHOn  = "CH_ON"
	EventCHOff = "CH_OFF"
	EventHWOn  = "HW_ON"
	EventHWOff = "HW_OFF"
)

// System events seen on the system topic. The vocabulary is boiler-sensor's
// and may grow; DecodeSystem accepts any non-empty event.
const (
	SystemStartup     = "STARTUP"
	SystemHeartbeat   = "HEARTBEAT"
	SystemShutdown    = "SHUTDOWN"
	SystemReconnected = "RECONNECTED"
)

// Event is one programmer switch transition, with both channels' states
// after it.
type Event struct {
	Timestamp time.Time
	Event     string // CH_ON, CH_OFF, HW_ON or HW_OFF
	HW, CH    Switch
}

// DecodeEvent decodes an `energy/boiler/sensor/events` payload.
func DecodeEvent(b []byte) (Event, error) { return decodeEvent(b) }

// Shape is which of the system topic's two shapes a message had.
type Shape string

// System message shapes.
const (
	ShapeStatus Shape = "status" // full snapshot: STARTUP, HEARTBEAT, SHUTDOWN
	ShapeSystem Shape = "system" // compact: RECONNECTED, the last will
)

// System is an `energy/boiler/sensor/system` message.
type System struct {
	Shape Shape
	Event string
	// Timestamp is the message's own. In the last will it is when
	// boiler-sensor connected (a will is fixed then), so it can be weeks old.
	Timestamp time.Time
	// HW and CH are Unknown unless the message carries them (only the
	// status shape does). While Ready is false they come from before the
	// GPIO baseline and shouldn't be trusted.
	HW, CH    Switch
	Ready     *bool      // status shape only
	UptimeS   *int64     // status shape only
	StartTime *time.Time // status shape only
	Reason    string     // e.g. MQTT_DISCONNECT in the last will
	Source    string     // e.g. last_will
	// HeartbeatMS is config.heartbeat_ms, the heartbeat interval, when the
	// message carries it (status shape; the fixtures strip it).
	HeartbeatMS *int64
}

// IsHeartbeat says whether this is a HEARTBEAT snapshot.
func (s System) IsHeartbeat() bool { return s.Shape == ShapeStatus && s.Event == SystemHeartbeat }

// ReadyHeartbeat says whether s says what HW and CH are: a HEARTBEAT with
// ready true. Anything else (RECONNECTED, the last will, STARTUP, a
// heartbeat before the GPIO baseline) doesn't.
func (s System) ReadyHeartbeat() bool { return s.IsHeartbeat() && s.Ready != nil && *s.Ready }

// DecodeSystem decodes an `energy/boiler/sensor/system` payload.
func DecodeSystem(b []byte) (System, error) { return decodeSystem(b) }
