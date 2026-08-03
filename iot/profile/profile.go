// Package profile defines the per-binary sensor configuration that drives the
// generic engine. A SensorProfile is plain data: it names the logical channels
// a device tracks, the GPIO pins they map to, the events they emit, and the
// MQTT/web vocabulary used to render them.
//
// This package has NO behaviour — it is the "what" (boiler vs doorbell), not
// the "how" (debounce, baseline, publishing). The engine packages (gpio, logic,
// mqtt, status, web) all read a profile to stay sensor-agnostic.
package profile

import "time"

// Channel is a single logical input the engine tracks. One per GPIO line.
type Channel struct {
	// Key is the stable identifier used as the JSON object key and the map key
	// in engine state, e.g. "ch", "hw", "button".
	Key string
	// Label is the human-readable name for the web UI, e.g. "Central Heating".
	Label string
	// Pin is the BCM pin number.
	Pin int
	// ActiveLow inverts the raw GPIO reading: when true, a raw active line (1)
	// means logical OFF (e.g. optocoupler wiring). Applied in package gpio.
	ActiveLow bool
	// OnEvent is the event name emitted on an OFF->ON transition, e.g. "CH_ON".
	OnEvent string
	// OffEvent is the event name emitted on an ON->OFF transition, e.g. "CH_OFF".
	OffEvent string
	// Debounce is the per-channel debounce duration. If zero, the engine's
	// profile-wide default debounce is used.
	Debounce time.Duration
	// Pull selects the internal bias resistor for the GPIO line. The default
	// (PullDown) matches the Pi boot state; a button wired to GND needs PullUp
	// so the line idles high when not pressed.
	Pull PullMode
}

// PullMode selects the internal bias resistor applied to a GPIO input line.
type PullMode int

const (
	// PullDown is the default: the line idles low. Matches the Pi's boot
	// defaults.
	PullDown PullMode = iota
	// PullUp idles the line high; used for buttons wired to GND.
	PullUp
)

// SensorProfile is the per-binary domain configuration. Everything that says
// "boiler/CH/HW" or "doorbell/PRESS" lives here, supplied by the cmd/*-sensor
// binary, never hardcoded in the shared engine packages.
type SensorProfile struct {
	// Channels are the logical inputs, in canonical order. This order defines
	// GPIO read order, event emission order when multiple channels transition
	// in the same tick, and JSON key order in the event payload.
	Channels []Channel
	// EventTopic is the MQTT topic for state-transition events.
	EventTopic string
	// SystemTopic is the MQTT topic for lifecycle/system events.
	SystemTopic string
	// PayloadRoot is the JSON root key for event payloads, e.g. "boiler".
	PayloadRoot string
	// WebTitle is the HTML status page title.
	WebTitle string
}

// Keys returns the channel keys in profile order.
func (p SensorProfile) Keys() []string {
	keys := make([]string, len(p.Channels))
	for i, c := range p.Channels {
		keys[i] = c.Key
	}
	return keys
}

// Channel returns the channel with the given key and whether it was found.
func (p SensorProfile) Channel(key string) (Channel, bool) {
	for _, c := range p.Channels {
		if c.Key == key {
			return c, true
		}
	}
	return Channel{}, false
}
