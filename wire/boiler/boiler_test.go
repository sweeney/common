package boiler

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Payloads are shaped like boiler-sensor's (README and its wire-contract
// test) with made-up values: no real broker, network or times.

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func bp(b bool) *bool    { return &b }
func i64(i int64) *int64 { return &i }
func tp(s string) *time.Time {
	t := ts(s)
	return &t
}

const heartbeat = `{"status":{"event":"HEARTBEAT","ch":"OFF","hw":"ON","ready":true,"uptime_seconds":900,` +
	`"start_time":"2001-01-01T12:00:00Z","timestamp":"2001-01-01T12:15:00Z",` +
	`"mqtt":{"connected":true,"broker":"tcp://broker.example:1883"},` +
	`"event_counts":{"ch_on":1,"ch_off":1,"hw_on":1,"hw_off":0},` +
	`"network":{"type":"wifi","status":"connected"},` +
	`"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":900000}}}`

func TestDecodeEvent(t *testing.T) {
	cases := []struct {
		in   string
		want Event
	}{
		{`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"CH_ON","ch":{"state":"ON"},"hw":{"state":"OFF"}}}`,
			Event{Timestamp: ts("2001-01-01T06:30:01Z"), Event: EventCHOn, CH: On, HW: Off}},
		{`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"HW_ON","ch":{"state":"ON"},"hw":{"state":"ON"}}}`,
			Event{Timestamp: ts("2001-01-01T06:30:01Z"), Event: EventHWOn, CH: On, HW: On}},
		{`{"boiler":{"timestamp":"2001-01-01T07:45:01Z","event":"HW_OFF","ch":{"state":"OFF"},"hw":{"state":"OFF"}}}`,
			Event{Timestamp: ts("2001-01-01T07:45:01Z"), Event: EventHWOff, CH: Off, HW: Off}},
		// a channel without its state is unknown, never off
		{`{"boiler":{"timestamp":"2001-01-01T07:15:01Z","event":"CH_OFF","ch":{"state":"OFF"}}}`,
			Event{Timestamp: ts("2001-01-01T07:15:01Z"), Event: EventCHOff, CH: Off, HW: Unknown}},
		{`{"boiler":{"timestamp":"2001-01-01T07:15:01Z","event":"CH_OFF","ch":{"state":"OFF"},"hw":{"state":"UNKNOWN"},"extra":1}}`,
			Event{Timestamp: ts("2001-01-01T07:15:01Z"), Event: EventCHOff, CH: Off, HW: Unknown}},
		{`{"boiler":{"timestamp":"2001-01-01T07:15:01Z","event":"CH_OFF","ch":{"state":"OFF"},"hw":null}}`,
			Event{Timestamp: ts("2001-01-01T07:15:01Z"), Event: EventCHOff, CH: Off, HW: Unknown}},
	}
	for _, c := range cases {
		got, err := DecodeEvent([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

func TestDecodeEventErrors(t *testing.T) {
	for _, in := range []string{
		``, `null`, `[]`, `{}`, `{"boiler":null}`, `{"boiler":1}`,
		`{"boiler":{"event":"CH_ON","ch":{"state":"ON"}}}`,                                       // no timestamp
		`{"boiler":{"timestamp":"soon","event":"CH_ON","ch":{"state":"ON"}}}`,                    // bad timestamp
		`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","ch":{"state":"ON"}}}`,                    // no event
		`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"DHW_ON","ch":{"state":"ON"}}}`,   // unknown event
		`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"CH_ON","ch":{"state":"MAYBE"}}}`, // bad state
		`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"CH_ON","ch":"ON"}}`,              // state not an object
		`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"CH_ON","ch":{"state":"OFF"}}}`,   // contradicts its event
		`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"HW_OFF","hw":{"state":"ON"}}}`,   // contradicts its event
		`{"boiler":{"timestamp":"2001-01-01T06:30:01Z","event":"HW_OFF"}}`,                       // its own channel missing
	} {
		if got, err := DecodeEvent([]byte(in)); err == nil {
			t.Errorf("%q: no error, got %+v", in, got)
		}
	}
}

func TestDecodeSystemHeartbeat(t *testing.T) {
	got, err := DecodeSystem([]byte(heartbeat))
	if err != nil {
		t.Fatal(err)
	}
	want := System{Shape: ShapeStatus, Event: SystemHeartbeat, Timestamp: ts("2001-01-01T12:15:00Z"),
		HW: On, CH: Off, Ready: bp(true), UptimeS: i64(900), StartTime: tp("2001-01-01T12:00:00Z"), HeartbeatMS: i64(900000)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if !got.IsHeartbeat() {
		t.Error("IsHeartbeat false")
	}
}

func TestDecodeSystemStartupNotReady(t *testing.T) {
	in := strings.NewReplacer(`"HEARTBEAT"`, `"STARTUP"`, `"ready":true`, `"ready":false`, `"hw":"ON"`, `"hw":"UNKNOWN"`).Replace(heartbeat)
	got, err := DecodeSystem([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got.Event != SystemStartup || got.Ready == nil || *got.Ready || got.HW != Unknown || got.CH != Off || got.IsHeartbeat() {
		t.Errorf("got %+v", got)
	}
}

// After a reconnect the retained system message can be RECONNECTED: it
// decodes, but carries no switch states.
func TestDecodeSystemReconnected(t *testing.T) {
	got, err := DecodeSystem([]byte(`{"system":{"timestamp":"2001-01-02T01:09:02Z","event":"RECONNECTED"}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := System{Shape: ShapeSystem, Event: SystemReconnected, Timestamp: ts("2001-01-02T01:09:02Z")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if got.IsHeartbeat() || got.HW != Unknown || got.CH != Unknown {
		t.Errorf("RECONNECTED must not look like a heartbeat or carry HW/CH: %+v", got)
	}
}

func TestDecodeSystemLastWill(t *testing.T) {
	got, err := DecodeSystem([]byte(`{"system":{"timestamp":"2001-01-01T12:00:00Z","event":"SHUTDOWN","reason":"MQTT_DISCONNECT","source":"last_will"}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := System{Shape: ShapeSystem, Event: SystemShutdown, Timestamp: ts("2001-01-01T12:00:00Z"),
		Reason: "MQTT_DISCONNECT", Source: "last_will"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// A compact message that does carry states (not seen yet, but allowed)
// keeps them.
func TestDecodeSystemCompactWithStates(t *testing.T) {
	got, err := DecodeSystem([]byte(`{"system":{"timestamp":"2001-01-01T12:00:00Z","event":"RECONNECTED","hw":"OFF"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.HW != Off || got.CH != Unknown {
		t.Errorf("got %+v", got)
	}
}

func TestDecodeSystemErrors(t *testing.T) {
	for _, in := range []string{
		``, `null`, `{}`, `[]`, `{"status":null}`, `{"other":{}}`,
		`{"status":{},"system":{}}`, // both shapes
		`{"system":{"event":"RECONNECTED"}}`,
		`{"system":{"timestamp":"2001-01-01T12:00:00Z"}}`,
		`{"system":{"timestamp":"2001-01-01T12:00:00Z","event":""}}`,
		`{"system":{"timestamp":"never","event":"RECONNECTED"}}`,
		strings.Replace(heartbeat, `"hw":"ON"`, `"hw":"YES"`, 1),
		strings.Replace(heartbeat, `"ready":true`, `"ready":"true"`, 1),
		strings.Replace(heartbeat, `"uptime_seconds":900`, `"uptime_seconds":-1.5`, 1),
		strings.Replace(heartbeat, `"start_time":"2001-01-01T12:00:00Z"`, `"start_time":"x"`, 1),
		strings.Replace(heartbeat, `"timestamp":"2001-01-01T12:15:00Z"`, `"timestamp":null`, 1),
		heartbeat + "x",
	} {
		if got, err := DecodeSystem([]byte(in)); err == nil {
			t.Errorf("%q: no error, got %+v", in, got)
		}
	}
}

func TestSwitchString(t *testing.T) {
	if Unknown.String() != "unknown" || Off.String() != "off" || On.String() != "on" {
		t.Error(Unknown, Off, On)
	}
	var zero Switch
	if zero != Unknown {
		t.Error("zero Switch is not Unknown")
	}
}

// A heartbeat without its config block (the fixtures keep only the state
// fields) has no interval; one that isn't an integer is an error.
func TestDecodeSystemHeartbeatInterval(t *testing.T) {
	got, err := DecodeSystem([]byte(`{"status":{"event":"HEARTBEAT","ch":"OFF","hw":"OFF","ready":true,"timestamp":"2001-01-01T12:15:00Z"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.HeartbeatMS != nil {
		t.Errorf("heartbeat_ms %v without a config block, want nil", *got.HeartbeatMS)
	}
	if _, err := DecodeSystem([]byte(strings.Replace(heartbeat, `"heartbeat_ms":900000`, `"heartbeat_ms":"15m"`, 1))); err == nil {
		t.Error("heartbeat_ms as a string: no error")
	}
}

// ReadyHeartbeat is the one rule for whether a system message says what HW
// and CH are: a HEARTBEAT with ready true.
func TestReadyHeartbeat(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    System
		want bool
	}{
		{"ready heartbeat", System{Shape: ShapeStatus, Event: SystemHeartbeat, Ready: bp(true)}, true},
		{"not ready", System{Shape: ShapeStatus, Event: SystemHeartbeat, Ready: bp(false)}, false},
		{"no ready", System{Shape: ShapeStatus, Event: SystemHeartbeat}, false},
		{"startup", System{Shape: ShapeStatus, Event: SystemStartup, Ready: bp(true)}, false},
		{"reconnected", System{Shape: ShapeSystem, Event: SystemReconnected}, false},
	} {
		if got := tc.s.ReadyHeartbeat(); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
