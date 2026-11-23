// Package tanktemp decodes tanktemp's MQTT messages: tank state, the daemon's
// status (and last will), and the water meter. tanktemp's docs/WIRE.md is the
// normative contract; the copies of its goldens in testdata/ pin it here.
//
// Decoding follows WIRE.md for consumers:
//   - an unknown value is JSON null and decodes to a nil pointer, never 0;
//   - every key is always present (except status `reason`, and the last will,
//     which is exactly online and event), so a missing key is an error;
//   - except status `meters`, which builds before the meter (before
//     2026-11-20, still in the recordings) don't send: missing decodes to nil
//     (unknown), distinct from `[]` (no meters); `null` is still an error;
//   - new fields are additive, so unknown keys are accepted and ignored;
//   - a closed vocabulary (event, trigger, level) outside its values is an
//     error, as is anything that isn't the documented type;
//   - status `online` must match its event: true for STARTUP and HEARTBEAT,
//     false for SHUTDOWN and the last will;
//   - a sensor's temp_c and raw_c are both null (the read failed, and error
//     is non-null) or both set (and error is null). error's vocabulary is
//     open: an unknown code is accepted, so a new one doesn't drop a sweep.
//
// A message that fails to decode is an error, never a zero value.
//
// It was written for washhouse (its first consumer) and moved here so
// statehouse's adapters can share it.
package tanktemp

import "time"

// State is one tank's sweep (WIRE.md, State).
type State struct {
	DeviceID    string
	Timestamp   time.Time
	ClockSynced bool
	SweepMS     int64
	Sensors     []Sensor
}

// Sensor is one probe in a State, bottom (order 1) first.
type Sensor struct {
	Position string
	Order    int
	HeightMM *int // nil: not configured
	ROM      string
	TempC    *float64 // nil: the read failed
	RawC     *float64 // nil: the read failed
	OffsetC  float64
	Error    *string // nil, or why there is no value
	Suspect  *string // nil, or "85c_unverified"
}

// DecodeState decodes a `{prefix}/{device_id}/state` payload.
func DecodeState(b []byte) (State, error) { return decodeState(b) }

// Event is a status message's event.
type Event string

// Status events.
const (
	EventStartup   Event = "STARTUP"
	EventHeartbeat Event = "HEARTBEAT"
	EventShutdown  Event = "SHUTDOWN"
	EventLWT       Event = "LWT"
)

// Status is a `{prefix}/status` message (WIRE.md, Status). The last will
// carries only Online (false) and Event (LWT): every other field is zero and
// Timestamp is nil.
type Status struct {
	Online    bool
	Event     Event
	Timestamp *time.Time // nil only in the last will
	Version   string
	Ready     bool
	UptimeS   int64
	Tanks     []string
	// Meters is nil when the build predates the meter (no `meters` key),
	// and an empty, non-nil slice when none is configured.
	Meters []string
	// SensorsOK and SensorsFailed are nil before the first sweep.
	SensorsOK, SensorsFailed *int
	// Unexpected is nil before the first sweep (null on the wire), and an
	// empty, non-nil slice when there are none.
	Unexpected      []string
	StartupProblems []Problem
	Reason          string // SHUTDOWN only; "" otherwise
}

// Problem is one startup problem.
type Problem struct {
	ROM     *string // nil for a bus-wide problem, and for a meter
	Code    string
	Message string
}

// DecodeStatus decodes a `{prefix}/status` payload, including the last will.
func DecodeStatus(b []byte) (Status, error) { return decodeStatus(b) }

// Trigger is why a meter message was sent.
type Trigger string

// Meter triggers.
const (
	TriggerPulse Trigger = "pulse"
	TriggerOpen  Trigger = "open"
	TriggerSweep Trigger = "sweep"
)

// Level is the reed after debounce.
type Level string

// Reed levels.
const (
	LevelOpen   Level = "open"
	LevelClosed Level = "closed"
)

// Meter is a `{prefix}/meter` message (WIRE.md, Meter).
type Meter struct {
	DeviceID       string
	Timestamp      time.Time // to the second
	ClockSynced    bool
	Trigger        Trigger
	Seq            uint64
	LitresPerPulse float64
	Level          Level
	// CountingSince is the run: with Seq, the de-duplication key.
	CountingSince time.Time
	Count         uint64
	// LastPulseAt is the last counted closing, to the millisecond; nil
	// before the first.
	LastPulseAt *time.Time
	// LastIntervalMS is nil before the second litre; LastClosedMS is nil
	// until a counted litre has ended.
	LastIntervalMS, LastClosedMS *int64
	Noise, Short, Glitches, Lost uint64
}

// DecodeMeter decodes a `{prefix}/meter` payload.
func DecodeMeter(b []byte) (Meter, error) { return decodeMeter(b) }

// OpenedAt is when the reed opened to end the litre at LastPulseAt:
// LastPulseAt + LastClosedMS, exact to the millisecond (WIRE.md: use it, not
// the rounded Timestamp, for flow). Only an `open` message says this for
// certain: a pulse's LastClosedMS belongs to the litre before, and a sweep's
// may too while an opening is being held.
func (m Meter) OpenedAt() (time.Time, bool) {
	if m.Trigger != TriggerOpen || m.LastPulseAt == nil || m.LastClosedMS == nil {
		return time.Time{}, false
	}
	return m.LastPulseAt.Add(time.Duration(*m.LastClosedMS) * time.Millisecond), true
}

// Litres is Count × LitresPerPulse: the litres counted since CountingSince.
func (m Meter) Litres() float64 { return float64(m.Count) * m.LitresPerPulse }
