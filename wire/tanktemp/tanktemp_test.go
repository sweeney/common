package tanktemp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func tp(s string) *time.Time { t := ts(s); return &t }
func ip(i int) *int          { return &i }
func i64(i int64) *int64     { return &i }
func fp(f float64) *float64  { return &f }
func sp(s string) *string    { return &s }

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// want is what each golden must decode to. Every golden in testdata/ must
// have an entry, so a new one copied from tanktemp can't go untested.
var want = map[string]any{
	"state-ok.golden": State{
		DeviceID: "hot-water-cylinder", Timestamp: ts("2026-11-02T21:06:26Z"), ClockSynced: true, SweepMS: 3988,
		Sensors: []Sensor{
			{Position: "bottom", Order: 1, HeightMM: nil, ROM: "28-000011721ce9", TempC: fp(12.0625), RawC: fp(12.0625), OffsetC: 0},
			{Position: "top", Order: 2, HeightMM: ip(1500), ROM: "28-0000117287e6", TempC: fp(61.185), RawC: fp(61.375), OffsetC: -0.19},
		},
	},
	"state-failures.golden": State{
		DeviceID: "hot-water-cylinder", Timestamp: ts("2026-11-02T21:06:56Z"), ClockSynced: false, SweepMS: 6402,
		Sensors: []Sensor{
			{Position: "bottom", Order: 1, HeightMM: ip(100), ROM: "28-000011721ce9", Error: sp("crc")},
			{Position: "middle", Order: 2, HeightMM: ip(800), ROM: "28-000011721ab6", OffsetC: 0.05, Error: sp("not_present")},
			{Position: "top", Order: 3, HeightMM: ip(1500), ROM: "28-0000117287e6", TempC: fp(85), RawC: fp(85), Suspect: sp("85c_unverified")},
		},
	},
	"status-heartbeat.golden": Status{
		Online: true, Event: EventHeartbeat, Timestamp: tp("2026-11-03T21:00:00Z"), Version: "4aba4d5", Ready: true,
		UptimeS: 86400, Tanks: []string{"hot-water-cylinder"}, Meters: []string{"hot_water_meter"},
		SensorsOK: ip(9), SensorsFailed: ip(1), Unexpected: []string{"28-000011721aa5"}, StartupProblems: []Problem{},
	},
	"status-shutdown.golden": Status{
		Online: false, Event: EventShutdown, Timestamp: tp("2026-11-03T21:00:05Z"), Version: "4aba4d5", Ready: true,
		UptimeS: 86405, Tanks: []string{"hot-water-cylinder"}, Meters: []string{},
		SensorsOK: ip(10), SensorsFailed: ip(0), Unexpected: []string{}, StartupProblems: []Problem{}, Reason: "SIGTERM",
	},
	"status-startup.golden": Status{
		Online: true, Event: EventStartup, Timestamp: tp("2026-11-02T21:00:00Z"), Version: "4aba4d5", Ready: false,
		UptimeS: 0, Tanks: []string{"hot-water-cylinder", "second"}, Meters: []string{"hot_water_meter"},
		SensorsOK: nil, SensorsFailed: nil, Unexpected: nil,
		StartupProblems: []Problem{
			{ROM: sp("28-0000117287e6"), Code: "parasite_power", Message: "28-0000117287e6 is in parasite-power mode; wire VDD to 3.3 V"},
			{ROM: nil, Code: "discover_failed", Message: "no 1-Wire bus masters"},
			{ROM: nil, Code: "gpio_request_failed", Message: "meter hot_water_meter: GPIO 27 on gpiochip0: permission denied"},
		},
	},
	"lwt.golden": Status{Online: false, Event: EventLWT},
	"meter-open.golden": Meter{
		DeviceID: "hot_water_meter", Timestamp: ts("2026-11-20T07:12:58Z"), ClockSynced: true, Trigger: TriggerOpen,
		Seq: 3671, LitresPerPulse: 1, Level: LevelOpen, CountingSince: ts("2026-11-19T22:41:07Z"), Count: 1833,
		LastPulseAt: tp("2026-11-20T07:12:57.312Z"), LastIntervalMS: i64(6699), LastClosedMS: i64(1541),
		Noise: 15, Short: 1, Glitches: 2, Lost: 3,
	},
	"meter-pulse.golden": Meter{
		DeviceID: "hot_water_meter", Timestamp: ts("2026-11-20T07:05:04Z"), ClockSynced: true, Trigger: TriggerPulse,
		Seq: 1, LitresPerPulse: 1, Level: LevelClosed, CountingSince: ts("2026-11-20T07:00:41Z"), Count: 1,
		LastPulseAt: tp("2026-11-20T07:05:04.007Z"),
	},
	"meter-sweep-none.golden": Meter{
		DeviceID: "hot_water_meter", Timestamp: ts("2026-11-20T01:30:00Z"), ClockSynced: false, Trigger: TriggerSweep,
		Seq: 7, LitresPerPulse: 10, Level: LevelClosed, CountingSince: ts("2026-11-20T01:27:00Z"), Count: 0,
	},
}

func decodeAny(name string, b []byte) (any, error) {
	switch {
	case strings.HasPrefix(name, "state-"):
		return DecodeState(b)
	case strings.HasPrefix(name, "status-"), name == "lwt.golden":
		return DecodeStatus(b)
	case strings.HasPrefix(name, "meter-"):
		return DecodeMeter(b)
	}
	return nil, nil
}

func TestEveryGoldenDecodes(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(want) {
		t.Errorf("%d goldens in testdata, %d expectations", len(files), len(want))
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			exp, ok := want[name]
			if !ok {
				t.Fatalf("no expectation for %s", name)
			}
			got, err := decodeAny(name, golden(t, name))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(got, exp) {
				t.Errorf("got\n%#v\nwant\n%#v", got, exp)
			}
		})
	}
}

// Times come back in UTC, so they compare with == as well as Equal.
func TestTimesAreUTC(t *testing.T) {
	m, err := DecodeMeter(golden(t, "meter-open.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Timestamp.Location() != time.UTC || m.LastPulseAt.Location() != time.UTC || m.CountingSince.Location() != time.UTC {
		t.Errorf("times not UTC: %v %v %v", m.Timestamp.Location(), m.LastPulseAt.Location(), m.CountingSince.Location())
	}
}

// removeKey deletes one top-level key from a compact golden.
func removeKey(t *testing.T, b []byte, key string) []byte {
	t.Helper()
	s := string(b)
	i := strings.Index(s, `"`+key+`":`)
	if i < 0 {
		t.Fatalf("no key %s", key)
	}
	// find the end of the value: the next top-level comma or closing brace
	depth, j := 0, i+len(key)+3
	inStr := false
	for ; j < len(s); j++ {
		c := s[j]
		if inStr {
			if c == '\\' {
				j++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[', '{':
			depth++
		case ']', '}':
			if depth == 0 {
				return []byte(strings.TrimSuffix(s[:i], ",") + s[j:])
			}
			depth--
		case ',':
			if depth == 0 {
				return []byte(s[:i] + s[j+1:])
			}
		}
	}
	t.Fatalf("unterminated value for %s", key)
	return nil
}

func TestMissingKeyIsAnError(t *testing.T) {
	cases := map[string][]string{
		"state-ok.golden":         {"device_id", "timestamp", "clock_synced", "sweep_ms", "sensors"},
		"status-heartbeat.golden": {"online", "event", "timestamp", "version", "ready", "uptime_s", "tanks", "sensors_ok", "sensors_failed", "unexpected", "startup_problems"},
		"meter-open.golden": {"device_id", "timestamp", "clock_synced", "trigger", "seq", "litres_per_pulse", "level",
			"counting_since", "count", "last_pulse_at", "last_interval_ms", "last_closed_ms", "noise", "short", "glitches", "lost"},
		"lwt.golden": {"online", "event"},
	}
	for name, keys := range cases {
		for _, k := range keys {
			b := removeKey(t, golden(t, name), k)
			if _, err := decodeAny(name, b); err == nil {
				t.Errorf("%s without %s: no error\n%s", name, k, b)
			}
		}
	}
}

// Builds before the meter (before 2026-11-20) send status without `meters`:
// that is unknown (nil), not "no meters" ([]), and everything else decodes.
// `meters: null` is still refused: a build with the key never sends null.
func TestMissingMetersIsUnknown(t *testing.T) {
	for _, name := range []string{"status-heartbeat.golden", "status-startup.golden", "status-shutdown.golden"} {
		got, err := DecodeStatus(removeKey(t, golden(t, name), "meters"))
		if err != nil {
			t.Errorf("%s without meters: %v", name, err)
			continue
		}
		exp := want[name].(Status)
		exp.Meters = nil
		if !reflect.DeepEqual(got, exp) {
			t.Errorf("%s without meters: got\n%#v\nwant\n%#v", name, got, exp)
		}
	}
	b := strings.Replace(string(golden(t, "status-heartbeat.golden")), `"meters":["hot_water_meter"]`, `"meters":null`, 1)
	if _, err := DecodeStatus([]byte(b)); err == nil {
		t.Error("meters null: no error")
	}
}

func TestMissingSensorKeyIsAnError(t *testing.T) {
	b := strings.Replace(string(golden(t, "state-ok.golden")), `"height_mm":null,`, ``, 1)
	if _, err := DecodeState([]byte(b)); err == nil {
		t.Error("sensor without height_mm: no error")
	}
	b = strings.Replace(string(golden(t, "status-startup.golden")), `"rom":null,`, ``, 1)
	if _, err := DecodeStatus([]byte(b)); err == nil {
		t.Error("problem without rom: no error")
	}
}

// New fields are additive (WIRE.md): a consumer must accept them.
func TestUnknownKeysAreAccepted(t *testing.T) {
	for name := range want {
		b := golden(t, name)
		s := strings.TrimSpace(string(b))
		s = s[:len(s)-1] + `,"future_field":{"x":[1,null]}}`
		got, err := decodeAny(name, []byte(s))
		if err != nil {
			t.Errorf("%s with an extra key: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got, want[name]) {
			t.Errorf("%s with an extra key decoded differently", name)
		}
	}
	b := strings.Replace(string(golden(t, "state-ok.golden")), `"suspect":null}`, `"suspect":null,"new":1}`, 1)
	if _, err := DecodeState([]byte(b)); err != nil {
		t.Errorf("sensor with an extra key: %v", err)
	}
}

// error's vocabulary is open (WIRE.md lists today's codes): a new code is
// still a failed read, not a reason to drop the sweep.
func TestUnknownSensorErrorIsAccepted(t *testing.T) {
	b := strings.Replace(string(golden(t, "state-failures.golden")), `"error":"crc"`, `"error":"some_new_code"`, 1)
	s, err := DecodeState([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Sensors[0].Error; e == nil || *e != "some_new_code" || s.Sensors[0].TempC != nil {
		t.Errorf("sensor 0 = %#v", s.Sensors[0])
	}
}

func TestBadPayloadsAreErrors(t *testing.T) {
	meter := string(golden(t, "meter-open.golden"))
	status := string(golden(t, "status-heartbeat.golden"))
	state := string(golden(t, "state-ok.golden"))
	shutdown := string(golden(t, "status-shutdown.golden"))
	startup := string(golden(t, "status-startup.golden"))
	failures := string(golden(t, "state-failures.golden"))
	cases := []struct {
		name, kind, payload string
	}{
		{"empty", "meter", ""},
		{"not json", "meter", "hello"},
		{"array", "meter", "[1,2]"},
		{"null", "meter", "null"},
		{"truncated", "meter", meter[:40]},
		{"trailing garbage", "meter", meter + "x"},
		{"unknown trigger", "meter", strings.Replace(meter, `"trigger":"open"`, `"trigger":"close"`, 1)},
		{"unknown level", "meter", strings.Replace(meter, `"level":"open"`, `"level":"ajar"`, 1)},
		{"count negative", "meter", strings.Replace(meter, `"count":1833`, `"count":-1`, 1)},
		{"count fractional", "meter", strings.Replace(meter, `"count":1833`, `"count":1833.5`, 1)},
		{"count string", "meter", strings.Replace(meter, `"count":1833`, `"count":"1833"`, 1)},
		{"count null", "meter", strings.Replace(meter, `"count":1833`, `"count":null`, 1)},
		{"seq zero", "meter", strings.Replace(meter, `"seq":3671`, `"seq":0`, 1)},
		{"noise null", "meter", strings.Replace(meter, `"noise":15`, `"noise":null`, 1)},
		{"litres_per_pulse zero", "meter", strings.Replace(meter, `"litres_per_pulse":1`, `"litres_per_pulse":0`, 1)},
		{"timestamp bad", "meter", strings.Replace(meter, `"timestamp":"2026-11-20T07:12:58Z"`, `"timestamp":"yesterday"`, 1)},
		{"timestamp null", "meter", strings.Replace(meter, `"timestamp":"2026-11-20T07:12:58Z"`, `"timestamp":null`, 1)},
		{"counting_since bad", "meter", strings.Replace(meter, `"counting_since":"2026-11-19T22:41:07Z"`, `"counting_since":""`, 1)},
		{"last_pulse_at bad", "meter", strings.Replace(meter, `"last_pulse_at":"2026-11-20T07:12:57.312Z"`, `"last_pulse_at":"07:12"`, 1)},
		{"device_id empty", "meter", strings.Replace(meter, `"device_id":"hot_water_meter"`, `"device_id":""`, 1)},
		{"clock_synced null", "meter", strings.Replace(meter, `"clock_synced":true`, `"clock_synced":null`, 1)},
		{"unknown event", "status", strings.Replace(status, `"event":"HEARTBEAT"`, `"event":"PING"`, 1)},
		{"online string", "status", strings.Replace(status, `"online":true`, `"online":"yes"`, 1)},
		{"tanks null", "status", strings.Replace(status, `"tanks":["hot-water-cylinder"]`, `"tanks":null`, 1)},
		{"status timestamp null", "status", strings.Replace(status, `"timestamp":"2026-11-03T21:00:00Z"`, `"timestamp":null`, 1)},
		{"lwt online true", "status", `{"online":true,"event":"LWT"}`},
		{"heartbeat with only lwt keys", "status", `{"online":true,"event":"HEARTBEAT"}`},
		// online: true from tanktemp; false in the last will and on SHUTDOWN.
		{"heartbeat online false", "status", strings.Replace(status, `"online":true`, `"online":false`, 1)},
		{"startup online false", "status", strings.Replace(startup, `"online":true`, `"online":false`, 1)},
		{"shutdown online true", "status", strings.Replace(shutdown, `"online":false`, `"online":true`, 1)},
		{"sensors null", "state", strings.Replace(state, `"sensors":[`, `"sensors":null,"x":[`, 1)},
		{"sensor not object", "state", `{"device_id":"a","timestamp":"2026-11-02T21:06:26Z","clock_synced":true,"sweep_ms":1,"sensors":[1]}`},
		{"state device_id empty", "state", strings.Replace(state, `"device_id":"hot-water-cylinder"`, `"device_id":""`, 1)},
		{"order string", "state", strings.Replace(state, `"order":1`, `"order":"1"`, 1)},
		// temp_c and raw_c are null exactly when the read failed, and error
		// then says why (WIRE.md, State).
		{"temp_c null, error null", "state", strings.Replace(state, `"temp_c":12.0625`, `"temp_c":null`, 1)},
		{"raw_c null, error null", "state", strings.Replace(state, `"raw_c":12.0625`, `"raw_c":null`, 1)},
		{"temp_c null, raw_c set, error set", "state", strings.Replace(failures, `"temp_c":null,"raw_c":null,"offset_c":0,"error":"crc"`, `"temp_c":null,"raw_c":12.5,"offset_c":0,"error":"crc"`, 1)},
		{"raw_c null, temp_c set, error set", "state", strings.Replace(failures, `"temp_c":null,"raw_c":null,"offset_c":0,"error":"crc"`, `"temp_c":12.5,"raw_c":null,"offset_c":0,"error":"crc"`, 1)},
		{"both null, error null", "state", strings.Replace(failures, `"error":"crc"`, `"error":null`, 1)},
		{"value with an error", "state", strings.Replace(state, `"offset_c":0,"error":null`, `"offset_c":0,"error":"crc"`, 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			var got any
			switch c.kind {
			case "meter":
				got, err = DecodeMeter([]byte(c.payload))
			case "status":
				got, err = DecodeStatus([]byte(c.payload))
			case "state":
				got, err = DecodeState([]byte(c.payload))
			}
			if err == nil {
				t.Errorf("no error; decoded %#v", got)
			}
			t.Log(err)
		})
	}
}

func TestMeterHelpers(t *testing.T) {
	m, err := DecodeMeter(golden(t, "meter-open.golden"))
	if err != nil {
		t.Fatal(err)
	}
	at, ok := m.OpenedAt()
	if !ok || !at.Equal(ts("2026-11-20T07:12:58.853Z")) {
		t.Errorf("OpenedAt = %v, %v; want 07:12:58.853", at, ok)
	}
	if got := m.Litres(); got != 1833 {
		t.Errorf("Litres = %v, want 1833", got)
	}
	s, err := DecodeMeter(golden(t, "meter-sweep-none.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.OpenedAt(); ok {
		t.Error("OpenedAt ok with no last pulse")
	}
	p, err := DecodeMeter(golden(t, "meter-pulse.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.OpenedAt(); ok {
		t.Error("OpenedAt ok on a pulse: its last_closed_ms is an earlier litre's")
	}
	s.Count = 3
	if got := s.Litres(); got != 30 {
		t.Errorf("Litres with K=10, count 3 = %v, want 30", got)
	}
}
