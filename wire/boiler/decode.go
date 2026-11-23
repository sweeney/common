package boiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func parseSwitch(s string) (Switch, error) {
	switch s {
	case "ON":
		return On, nil
	case "OFF":
		return Off, nil
	case "UNKNOWN", "":
		return Unknown, nil
	}
	return Unknown, fmt.Errorf("unknown switch state %q", s)
}

func parseTime(k, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", k, err)
	}
	return t.UTC(), nil
}

// inner returns the one object under key in b, which must be an object.
func inner(b []byte, key string) (json.RawMessage, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(b, &outer); err != nil {
		return nil, err
	}
	v, ok := outer[key]
	if !ok {
		return nil, fmt.Errorf("no %q object", key)
	}
	return v, nil
}

type channel struct {
	State *string `json:"state"`
}

func (c *channel) get() (Switch, error) {
	if c == nil || c.State == nil {
		return Unknown, nil
	}
	return parseSwitch(*c.State)
}

func decodeEvent(b []byte) (Event, error) {
	e, err := eventFrom(b)
	if err != nil {
		return Event{}, fmt.Errorf("boiler event: %w", err)
	}
	return e, nil
}

func eventFrom(b []byte) (Event, error) {
	raw, err := inner(b, "boiler")
	if err != nil {
		return Event{}, err
	}
	var w *struct {
		Timestamp *string  `json:"timestamp"`
		Event     string   `json:"event"`
		CH        *channel `json:"ch"`
		HW        *channel `json:"hw"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Event{}, err
	}
	if w == nil {
		return Event{}, errors.New("boiler is null")
	}
	if w.Timestamp == nil {
		return Event{}, errors.New("no timestamp")
	}
	var e Event
	if e.Timestamp, err = parseTime("timestamp", *w.Timestamp); err != nil {
		return Event{}, err
	}
	if e.CH, err = w.CH.get(); err != nil {
		return Event{}, fmt.Errorf("ch: %w", err)
	}
	if e.HW, err = w.HW.get(); err != nil {
		return Event{}, fmt.Errorf("hw: %w", err)
	}
	e.Event = w.Event
	// An event names a transition; the channel it names must be in the
	// state it says.
	var got, want Switch
	switch e.Event {
	case EventCHOn:
		got, want = e.CH, On
	case EventCHOff:
		got, want = e.CH, Off
	case EventHWOn:
		got, want = e.HW, On
	case EventHWOff:
		got, want = e.HW, Off
	default:
		return Event{}, fmt.Errorf("unknown event %q", e.Event)
	}
	if got != want {
		return Event{}, fmt.Errorf("%s with that channel %s", e.Event, got)
	}
	return e, nil
}

func decodeSystem(b []byte) (System, error) {
	s, err := systemFrom(b)
	if err != nil {
		return System{}, fmt.Errorf("boiler system: %w", err)
	}
	return s, nil
}

func systemFrom(b []byte) (System, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(b, &outer); err != nil {
		return System{}, err
	}
	st, hasStatus := outer["status"]
	sy, hasSystem := outer["system"]
	var s System
	var raw json.RawMessage
	switch {
	case hasStatus && hasSystem:
		return System{}, errors.New("both status and system objects")
	case hasStatus:
		s.Shape, raw = ShapeStatus, st
	case hasSystem:
		s.Shape, raw = ShapeSystem, sy
	default:
		return System{}, errors.New("neither a status nor a system object")
	}
	var w *struct {
		Event     string  `json:"event"`
		Timestamp *string `json:"timestamp"`
		CH        *string `json:"ch"`
		HW        *string `json:"hw"`
		Ready     *bool   `json:"ready"`
		UptimeS   *int64  `json:"uptime_seconds"`
		StartTime *string `json:"start_time"`
		Reason    string  `json:"reason"`
		Source    string  `json:"source"`
		Config    *struct {
			HeartbeatMS *int64 `json:"heartbeat_ms"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return System{}, err
	}
	if w == nil {
		return System{}, fmt.Errorf("%s is null", s.Shape)
	}
	if w.Event == "" {
		return System{}, errors.New("no event")
	}
	if w.Timestamp == nil {
		return System{}, errors.New("no timestamp")
	}
	var err error
	if s.Timestamp, err = parseTime("timestamp", *w.Timestamp); err != nil {
		return System{}, err
	}
	for _, c := range []struct {
		v   *string
		dst *Switch
		k   string
	}{{w.CH, &s.CH, "ch"}, {w.HW, &s.HW, "hw"}} {
		if c.v == nil {
			continue
		}
		if *c.dst, err = parseSwitch(*c.v); err != nil {
			return System{}, fmt.Errorf("%s: %w", c.k, err)
		}
	}
	if w.StartTime != nil {
		t, err := parseTime("start_time", *w.StartTime)
		if err != nil {
			return System{}, err
		}
		s.StartTime = &t
	}
	s.Event, s.Ready, s.UptimeS, s.Reason, s.Source = w.Event, w.Ready, w.UptimeS, w.Reason, w.Source
	if w.Config != nil {
		s.HeartbeatMS = w.Config.HeartbeatMS
	}
	return s, nil
}
