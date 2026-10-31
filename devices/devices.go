// Package devices is the schema of a devices_<site> namespace in the config
// service: one Device per device id. It owns every key the namespace holds,
// whichever service reads it; what a service does with a field stays in the
// service. See devices-DESIGN.md at the root of this repository.
package devices

import (
	"encoding/json"
	"fmt"
	"time"
)

// CoverageHouse is the sentinel meaning a device's readings describe the whole
// site rather than the room it sits in.
//
// It is also a legacy `location` value. That field carried two facts, usually
// a place but `house` always a scope, so it is never resolved as a room.
const CoverageHouse = "house"

// Device is one entry in a devices_<site> namespace, keyed by device id.
//
// The canonical identity is Scheme + Primary (and Display). IEEEAddress and
// FriendlyName are legacy Zigbee2MQTT shorthand that Normalise folds in.
//
// Device has no JSON decode method of its own: Decode decodes it field by
// field, so that one bad value costs only that field.
type Device struct {
	// Scheme names the adapter that owns the device ("zigbee", "tasmota",
	// "climate", ...). Primary is that adapter's stable identifier, Display
	// its human-readable name for the device.
	Scheme  string `yaml:"scheme"  json:"scheme,omitempty"`
	Primary string `yaml:"primary" json:"primary,omitempty"`
	Display string `yaml:"display" json:"display,omitempty"`

	IEEEAddress  string `yaml:"ieee_address"  json:"ieee_address,omitempty"`  // legacy
	FriendlyName string `yaml:"friendly_name" json:"friendly_name,omitempty"` // legacy

	Class       string `yaml:"class"        json:"class,omitempty"`
	DisplayName string `yaml:"display_name" json:"display_name,omitempty"`

	// Room is the floorplan room id the device sits in. Floor is declared
	// alongside it, not derived from the room id. Empty means unknown.
	Room  string `yaml:"room"  json:"room,omitempty"`
	Floor string `yaml:"floor" json:"floor,omitempty"`
	// Covers is what the readings describe when that is not the device's own
	// room: CoverageHouse, or another room id. Empty means its own room.
	Covers string `yaml:"covers" json:"covers,omitempty"`
	// Location is the deprecated free-text place, still decoded while
	// namespaces and services migrate on separate schedules.
	Location string `yaml:"location" json:"location,omitempty"`

	// The namespace's coordinate (where the device is in the house, to the
	// centimetre) and free-text note have no field, on purpose: no service
	// reads them, and a service cannot leak what it never holds. Decode
	// ignores them like any unknown key. The floorplan tooling owns them.

	// EnergyStrategy overrides the class's energy strategy for this device,
	// e.g. "integration" for a counter too coarse for its cycles.
	EnergyStrategy string      `yaml:"energy_strategy" json:"energy_strategy,omitempty"`
	Thresholds     *Thresholds `yaml:"thresholds"      json:"thresholds,omitempty"`
	// TargetTemperature is the temperature the device's space is kept at,
	// e.g. a freezer's -18.
	TargetTemperature *float64 `yaml:"target_temperature" json:"target_temperature,omitempty"`
	// EnvironmentFields are the environmental readings the device reports,
	// e.g. "temperature_c".
	EnvironmentFields []string `yaml:"environment_fields" json:"environment_fields,omitempty"`
}

// Thresholds are a device's activity-detection thresholds. Every field is a
// pointer so that an explicit zero is honoured rather than overridden by the
// class default. In JSON the durations are strings ("30s").
type Thresholds struct {
	IdleBelowW           *float64       `yaml:"idle_below_w"           json:"idle_below_w,omitempty"`
	ActiveAboveW         *float64       `yaml:"active_above_w"         json:"active_above_w,omitempty"`
	ActiveSustainedFor   *time.Duration `yaml:"active_sustained_for"   json:"active_sustained_for,omitempty"`
	InactiveSustainedFor *time.Duration `yaml:"inactive_sustained_for" json:"inactive_sustained_for,omitempty"`
	// CompressorAboveW starts an active cycle of a continuous_power_device
	// when power exceeds it.
	CompressorAboveW *float64 `yaml:"compressor_above_w" json:"compressor_above_w,omitempty"`
}

type thresholdsJSON struct {
	IdleBelowW           *float64 `json:"idle_below_w,omitempty"`
	ActiveAboveW         *float64 `json:"active_above_w,omitempty"`
	ActiveSustainedFor   *string  `json:"active_sustained_for,omitempty"`
	InactiveSustainedFor *string  `json:"inactive_sustained_for,omitempty"`
	CompressorAboveW     *float64 `json:"compressor_above_w,omitempty"`
}

// UnmarshalJSON reads the durations as strings, as statehouse always has.
func (t *Thresholds) UnmarshalJSON(b []byte) error {
	var raw thresholdsJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := Thresholds{IdleBelowW: raw.IdleBelowW, ActiveAboveW: raw.ActiveAboveW, CompressorAboveW: raw.CompressorAboveW}
	for _, f := range []struct {
		dst  **time.Duration
		src  *string
		name string
	}{
		{&out.ActiveSustainedFor, raw.ActiveSustainedFor, "active_sustained_for"},
		{&out.InactiveSustainedFor, raw.InactiveSustainedFor, "inactive_sustained_for"},
	} {
		if f.src == nil {
			continue
		}
		// An empty string is an explicit zero, as statehouse has always read it.
		var d time.Duration
		if *f.src != "" {
			var err error
			if d, err = time.ParseDuration(*f.src); err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
		}
		*f.dst = &d
	}
	*t = out
	return nil
}

// MarshalJSON writes the durations back as strings, so a decoded document
// round-trips.
func (t Thresholds) MarshalJSON() ([]byte, error) {
	str := func(d *time.Duration) *string {
		if d == nil {
			return nil
		}
		s := d.String()
		return &s
	}
	return json.Marshal(thresholdsJSON{
		IdleBelowW:           t.IdleBelowW,
		ActiveAboveW:         t.ActiveAboveW,
		ActiveSustainedFor:   str(t.ActiveSustainedFor),
		InactiveSustainedFor: str(t.InactiveSustainedFor),
		CompressorAboveW:     t.CompressorAboveW,
	})
}

// Place returns the room the device sits in: its Room, otherwise its
// deprecated Location, unless that is CoverageHouse, which names no room.
func (d Device) Place() string {
	if d.Room != "" {
		return d.Room
	}
	if d.Location == CoverageHouse {
		return ""
	}
	return d.Location
}

// Coverage returns what the device's readings describe when that is not its
// own room: Covers if set, else CoverageHouse if the legacy Location says so.
//
// The legacy spelling is honoured even once Room is set. A namespace
// mid-migration may publish room before it publishes covers, and dropping the
// coverage fact in that window would make the order of republishing matter.
func (d Device) Coverage() string {
	if d.Covers != "" {
		return d.Covers
	}
	if d.Location == CoverageHouse {
		return CoverageHouse
	}
	return ""
}

// CoversWholeSite reports whether the device's readings describe the whole
// site rather than a room.
func (d Device) CoversWholeSite() bool {
	return d.Coverage() == CoverageHouse
}

// Normalise folds the legacy Zigbee2MQTT shorthand into the canonical fields:
// ieee_address becomes scheme zigbee with that primary, and friendly_name the
// display. It fills only empty fields, so it is safe to call more than once.
// Decode calls it; a service loading devices any other way (local YAML) calls
// it itself.
func (d *Device) Normalise() {
	if d.Scheme == "" && (d.IEEEAddress != "" || d.FriendlyName != "") {
		d.Scheme = "zigbee"
	}
	if d.Primary == "" && d.IEEEAddress != "" {
		d.Primary = d.IEEEAddress
	}
	if d.Display == "" && d.FriendlyName != "" {
		d.Display = d.FriendlyName
	}
}
