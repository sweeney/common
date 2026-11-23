package tanktemp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// object parses b as one JSON object and checks its keys: every key in
// required must be present, and those not in nullable must not be null.
// Unknown keys are left alone (new fields are additive).
func object(b []byte, required, nullable []string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	for _, k := range required {
		v, ok := m[k]
		if !ok {
			return nil, fmt.Errorf("missing key %q", k)
		}
		if isNull(v) && !contains(nullable, k) {
			return nil, fmt.Errorf("%s is null", k)
		}
	}
	return m, nil
}

func isNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// field decodes m[k] into v.
func field(m map[string]json.RawMessage, k string, v any) error {
	if err := json.Unmarshal(m[k], v); err != nil {
		return fmt.Errorf("%s: %w", k, err)
	}
	return nil
}

func parseTime(k, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", k, err)
	}
	return t.UTC(), nil
}

func timeField(m map[string]json.RawMessage, k string) (time.Time, error) {
	var s string
	if err := field(m, k, &s); err != nil {
		return time.Time{}, err
	}
	return parseTime(k, s)
}

func optTimeField(m map[string]json.RawMessage, k string) (*time.Time, error) {
	if isNull(m[k]) {
		return nil, nil
	}
	t, err := timeField(m, k)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nonEmpty(k, s string) error {
	if s == "" {
		return fmt.Errorf("%s is empty", k)
	}
	return nil
}

// --- state

var stateKeys = []string{"device_id", "timestamp", "clock_synced", "sweep_ms", "sensors"}

var sensorKeys = []string{"position", "order", "height_mm", "rom", "temp_c", "raw_c", "offset_c", "error", "suspect"}
var sensorNullable = []string{"height_mm", "temp_c", "raw_c", "error", "suspect"}

func decodeState(b []byte) (State, error) {
	m, err := object(b, stateKeys, nil)
	if err != nil {
		return State{}, fmt.Errorf("tanktemp state: %w", err)
	}
	s, err := stateFrom(m)
	if err != nil {
		return State{}, fmt.Errorf("tanktemp state: %w", err)
	}
	return s, nil
}

func stateFrom(m map[string]json.RawMessage) (State, error) {
	var s State
	var err error
	if err = field(m, "device_id", &s.DeviceID); err != nil {
		return State{}, err
	}
	if err = nonEmpty("device_id", s.DeviceID); err != nil {
		return State{}, err
	}
	if s.Timestamp, err = timeField(m, "timestamp"); err != nil {
		return State{}, err
	}
	if err = field(m, "clock_synced", &s.ClockSynced); err != nil {
		return State{}, err
	}
	if err = field(m, "sweep_ms", &s.SweepMS); err != nil {
		return State{}, err
	}
	var raw []json.RawMessage
	if err = field(m, "sensors", &raw); err != nil {
		return State{}, err
	}
	s.Sensors = make([]Sensor, 0, len(raw))
	for i, r := range raw {
		sm, err := object(r, sensorKeys, sensorNullable)
		if err != nil {
			return State{}, fmt.Errorf("sensors[%d]: %w", i, err)
		}
		var x Sensor
		for _, f := range []struct {
			k string
			v any
		}{
			{"position", &x.Position}, {"order", &x.Order}, {"height_mm", &x.HeightMM}, {"rom", &x.ROM},
			{"temp_c", &x.TempC}, {"raw_c", &x.RawC}, {"offset_c", &x.OffsetC}, {"error", &x.Error}, {"suspect", &x.Suspect},
		} {
			if err := field(sm, f.k, f.v); err != nil {
				return State{}, fmt.Errorf("sensors[%d]: %w", i, err)
			}
		}
		if err := sensorInvariants(x); err != nil {
			return State{}, fmt.Errorf("sensors[%d]: %w", i, err)
		}
		s.Sensors = append(s.Sensors, x)
	}
	return s, nil
}

// sensorInvariants checks WIRE.md's pairing: temp_c and raw_c are null
// exactly when the read failed, and error is then why (non-null), else null.
// error's vocabulary is left open: a new code from tanktemp is still a
// failed read, and refusing it would drop the whole sweep.
func sensorInvariants(x Sensor) error {
	failed := x.TempC == nil
	if (x.RawC == nil) != failed {
		return errors.New("temp_c and raw_c: one null, one not")
	}
	if (x.Error != nil) != failed {
		if failed {
			return errors.New("no value and error null")
		}
		return fmt.Errorf("a value and error %q", *x.Error)
	}
	return nil
}

// --- status

var lwtKeys = []string{"online", "event"}

var statusKeys = []string{"online", "event", "timestamp", "version", "ready", "uptime_s", "tanks",
	"sensors_ok", "sensors_failed", "unexpected", "startup_problems"}
var statusNullable = []string{"sensors_ok", "sensors_failed", "unexpected"}

var problemKeys = []string{"rom", "code", "message"}

func decodeStatus(b []byte) (Status, error) {
	s, err := statusFrom(b)
	if err != nil {
		return Status{}, fmt.Errorf("tanktemp status: %w", err)
	}
	return s, nil
}

func statusFrom(b []byte) (Status, error) {
	m, err := object(b, lwtKeys, nil)
	if err != nil {
		return Status{}, err
	}
	var s Status
	if err := field(m, "online", &s.Online); err != nil {
		return Status{}, err
	}
	if err := field(m, "event", &s.Event); err != nil {
		return Status{}, err
	}
	switch s.Event {
	case EventLWT:
		if s.Online {
			return Status{}, errors.New("last will with online true")
		}
		return Status{Online: false, Event: EventLWT}, nil
	case EventStartup, EventHeartbeat:
		if !s.Online {
			return Status{}, fmt.Errorf("%s with online false", s.Event)
		}
	case EventShutdown:
		if s.Online {
			return Status{}, errors.New("SHUTDOWN with online true")
		}
	default:
		return Status{}, fmt.Errorf("unknown event %q", s.Event)
	}
	if m, err = object(b, statusKeys, statusNullable); err != nil {
		return Status{}, err
	}
	ts, err := timeField(m, "timestamp")
	if err != nil {
		return Status{}, err
	}
	s.Timestamp = &ts
	for _, f := range []struct {
		k string
		v any
	}{
		{"version", &s.Version}, {"ready", &s.Ready}, {"uptime_s", &s.UptimeS}, {"tanks", &s.Tanks},
		{"sensors_ok", &s.SensorsOK}, {"sensors_failed", &s.SensorsFailed},
		{"unexpected", &s.Unexpected},
	} {
		if err := field(m, f.k, f.v); err != nil {
			return Status{}, err
		}
	}
	// meters is absent from builds before the meter: unknown, so nil. When
	// present it is never null (`[]` with none).
	if v, ok := m["meters"]; ok {
		if isNull(v) {
			return Status{}, errors.New("meters is null")
		}
		if err := field(m, "meters", &s.Meters); err != nil {
			return Status{}, err
		}
	}
	if _, ok := m["reason"]; ok {
		if err := field(m, "reason", &s.Reason); err != nil {
			return Status{}, err
		}
	}
	var raw []json.RawMessage
	if err := field(m, "startup_problems", &raw); err != nil {
		return Status{}, err
	}
	s.StartupProblems = make([]Problem, 0, len(raw))
	for i, r := range raw {
		pm, err := object(r, problemKeys, []string{"rom"})
		if err != nil {
			return Status{}, fmt.Errorf("startup_problems[%d]: %w", i, err)
		}
		var p Problem
		for _, f := range []struct {
			k string
			v any
		}{{"rom", &p.ROM}, {"code", &p.Code}, {"message", &p.Message}} {
			if err := field(pm, f.k, f.v); err != nil {
				return Status{}, fmt.Errorf("startup_problems[%d]: %w", i, err)
			}
		}
		s.StartupProblems = append(s.StartupProblems, p)
	}
	return s, nil
}

// --- meter

var meterKeys = []string{"device_id", "timestamp", "clock_synced", "trigger", "seq", "litres_per_pulse", "level",
	"counting_since", "count", "last_pulse_at", "last_interval_ms", "last_closed_ms", "noise", "short", "glitches", "lost"}
var meterNullable = []string{"last_pulse_at", "last_interval_ms", "last_closed_ms"}

func decodeMeter(b []byte) (Meter, error) {
	m, err := meterFrom(b)
	if err != nil {
		return Meter{}, fmt.Errorf("tanktemp meter: %w", err)
	}
	return m, nil
}

func meterFrom(b []byte) (Meter, error) {
	m, err := object(b, meterKeys, meterNullable)
	if err != nil {
		return Meter{}, err
	}
	var x Meter
	for _, f := range []struct {
		k string
		v any
	}{
		{"device_id", &x.DeviceID}, {"clock_synced", &x.ClockSynced}, {"trigger", &x.Trigger}, {"seq", &x.Seq},
		{"litres_per_pulse", &x.LitresPerPulse}, {"level", &x.Level}, {"count", &x.Count},
		{"last_interval_ms", &x.LastIntervalMS}, {"last_closed_ms", &x.LastClosedMS},
		{"noise", &x.Noise}, {"short", &x.Short}, {"glitches", &x.Glitches}, {"lost", &x.Lost},
	} {
		if err := field(m, f.k, f.v); err != nil {
			return Meter{}, err
		}
	}
	if err := nonEmpty("device_id", x.DeviceID); err != nil {
		return Meter{}, err
	}
	switch x.Trigger {
	case TriggerPulse, TriggerOpen, TriggerSweep:
	default:
		return Meter{}, fmt.Errorf("unknown trigger %q", x.Trigger)
	}
	switch x.Level {
	case LevelOpen, LevelClosed:
	default:
		return Meter{}, fmt.Errorf("unknown level %q", x.Level)
	}
	if x.Seq == 0 {
		return Meter{}, errors.New("seq 0: seq counts from 1")
	}
	if !(x.LitresPerPulse > 0) {
		return Meter{}, fmt.Errorf("litres_per_pulse %v: must be > 0", x.LitresPerPulse)
	}
	if x.Timestamp, err = timeField(m, "timestamp"); err != nil {
		return Meter{}, err
	}
	if x.CountingSince, err = timeField(m, "counting_since"); err != nil {
		return Meter{}, err
	}
	if x.LastPulseAt, err = optTimeField(m, "last_pulse_at"); err != nil {
		return Meter{}, err
	}
	return x, nil
}
