package washhouse

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The goldens are the wire contract (docs/WIRE.md), written by hand from
// its examples before the encoders. A diff is a regression: never edit a
// golden to make a test pass; a change starts in WIRE.md.
func golden(t *testing.T, name string, got []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	want, rerr := os.ReadFile(filepath.Join("testdata", name))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n got %s\nwant %s", name, got, want)
	}
}

func at(s string) Time {
	x, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	if err != nil {
		panic(err)
	}
	return Time{x}
}

func atp(s string) *Time { x := at(s); return &x }
func n(x float64) *Num   { v := Num(x); return &v }
func b(x bool) *bool     { return &x }
func str(s string) *string {
	return &s
}
func i(x int) *int         { return &x }
func u(x uint64) *uint64   { return &x }
func src(s Source) *Source { return &s }

const left, right = "hot_water_cylinder_left", "hot_water_cylinder_right"

var since = at("2000-12-31T23:54:43.000Z")

func stateDraw() State {
	return State{
		Timestamp: at("2001-01-01T00:17:43.000Z"),
		StoredKWh: n(21.79), UsableL40: n(263.5), UsableL40Low: n(223.975),
		LowHotWater: b(false),
		Boiler:      Boiler{HW: b(false), CH: b(false)},
		AmbientC:    n(25.7),
		ActiveDraw: &ActiveDraw{DrawID: "20001231T235443Z-76", StartedAt: at("2001-01-01T00:10:10.308Z"),
			LastLitreAt: at("2001-01-01T00:17:40.112Z"), Litres: 48, FlowLPerMin: n(6.3), Kind: KindShower},
		Meter: Meter{Timestamp: atp("2001-01-01T00:17:43.000Z"), CountingSince: &since, Count: u(54),
			LitresSinceStart: n(54)},
		Tanktemp: Tanktemp{Timestamp: atp("2001-01-01T00:09:43.000Z"), Online: b(true), Version: str("4aba4d5")},
		Tanks: []TankSummary{
			{DeviceID: left, State: StateDrawing, StoredKWh: n(11.05), UsableL40: n(205.8), TopC: n(63.5)},
			{DeviceID: right, State: StateDrawing, StoredKWh: n(10.74), UsableL40: n(151.2), TopC: n(61.9)},
		},
	}
}

func stateUnknown() State {
	return State{Timestamp: at("2001-01-01T00:00:00.000Z"), Tanks: []TankSummary{
		{DeviceID: left, State: StateUnknown}, {DeviceID: right, State: StateUnknown}}}
}

func tankCooling() Tank {
	return Tank{DeviceID: right, Timestamp: at("2001-01-01T00:36:32.000Z"), SweepAt: atp("2001-01-01T00:36:32.000Z"),
		State: StateCooling, StoredKWh: n(8.315), MeanC: n(47.906), TopC: n(61.375), HotL: n(140.214),
		UsableL40: n(184.331), UsableL40Low: n(156.681), PowerKW: n(-1.33), UAWPerK: n(17), ProbesOK: i(5), ProbesTotal: i(5)}
}

func tankUnknown() Tank {
	return Tank{DeviceID: left, Timestamp: at("2001-01-01T00:00:00.000Z"), State: StateUnknown, UAWPerK: n(4)}
}

func drawStarted() Event {
	return Event{Event: EventDrawStarted, Timestamp: at("2001-01-01T00:10:10.308Z"),
		ActiveDraw: &ActiveDraw{DrawID: "20001231T235443Z-76", StartedAt: at("2001-01-01T00:10:10.308Z"),
			LastLitreAt: at("2001-01-01T00:10:10.308Z"), Litres: 1, Kind: KindTap}}
}

func drawEnded() Event {
	return Event{Event: EventDrawEnded, Timestamp: at("2001-01-01T00:18:54.000Z"),
		DrawEnded: &DrawEnded{DrawID: "20001231T235443Z-76", StartedAt: at("2001-01-01T00:10:10.308Z"),
			EndedAt: at("2001-01-01T00:17:46.608Z"), DurationS: 456.3, Litres: 50, VolumeKind: VolumeMetered,
			FlowLPerMin: n(6.297), Kind: KindShower,
			MeterFrom: MeterRef{CountingSince: since, Seq: 76}, MeterTo: MeterRef{CountingSince: since, Seq: 220}}}
}

func drawBalanced() Event {
	return Event{Event: EventDrawBalanced, Timestamp: at("2001-01-01T00:25:54.000Z"),
		DrawBalanced: &DrawBalanced{DrawID: "20001231T235443Z-76", EndedAt: at("2001-01-01T00:17:46.608Z"),
			Tanks: []TankShare{
				{DeviceID: left, Share: n(0.107), Litres: n(5.338), OutletPeakC: n(65.25), InletDropC: n(20.75)},
				{DeviceID: right, Share: n(0.893), Litres: n(44.662), OutletPeakC: n(62), InletDropC: n(24.5)},
			},
			OutletPeakC: n(62.347), InletDropC: n(24.5), EnergyKWh: n(2.924), HeatBalanceL: n(48.041),
			HeatBalanceReliable: true}}
}

func drawBalancedMasked() Event {
	return Event{Event: EventDrawBalanced, Timestamp: at("2001-01-01T00:23:56.000Z"),
		DrawBalanced: &DrawBalanced{DrawID: "20001231T150913Z-1784", EndedAt: at("2001-01-01T00:17:05.708Z"),
			Tanks: []TankShare{
				{DeviceID: left, OutletPeakC: n(71.938), InletDropC: n(35.438)},
				{DeviceID: right, OutletPeakC: n(68)},
			},
			OutletPeakC: n(70.235), InletDropC: n(35.438), EnergyKWh: n(4.012), HeatBalanceL: n(57.278),
			Reasons:        []Reason{ReasonHWOn, ReasonThermocline, ReasonInterrupted, ReasonSharesEarly},
			MaskedByReheat: true}}
}

func flowStarted() Event {
	return Event{Event: EventContinuousFlowStarted, Timestamp: at("2001-01-01T00:14:11.903Z"),
		ContinuousFlow: &ContinuousFlow{Since: at("2001-01-01T00:01:05.217Z"), Last: at("2001-01-01T00:14:11.903Z"),
			Litres: 2, FlowLPerMin: 0.076}}
}

func flowEnded() Event {
	return Event{Event: EventContinuousFlowEnded, Timestamp: at("2001-01-01T01:59:11.903Z"),
		ContinuousFlow: &ContinuousFlow{Since: at("2001-01-01T00:01:05.217Z"), Last: at("2001-01-01T01:14:11.903Z"),
			Litres: 6, FlowLPerMin: 0.082}}
}

func heatingStarted() Event {
	return Event{Event: EventHeatingStarted, Timestamp: at("2001-01-01T06:42:10.000Z"),
		Tank: &TankEvent{DeviceID: left, State: StateHeating, Prev: StateCooling, HeatSource: src(SourceHW), PowerKW: n(9.84)}}
}

func tankStateChanged() Event {
	return Event{Event: EventTankStateChanged, Timestamp: at("2001-01-01T00:10:10.308Z"),
		Tank: &TankEvent{DeviceID: right, State: StateDrawing, Prev: StateCooling}}
}

func statusHeartbeat() Status {
	return Status{Online: true, Event: StatusHeartbeat, Timestamp: at("2001-01-01T00:15:00.000Z"),
		Version: "e34968b", Ready: true, UptimeS: 900, Tanks: []string{left, right}}
}

func statusShutdown() Status {
	return Status{Online: false, Event: StatusShutdown, Timestamp: at("2001-01-01T02:00:00.000Z"),
		Version: "e34968b", Ready: true, UptimeS: 7200, Tanks: []string{left, right}, Reason: str("SIGTERM")}
}

func TestGoldenState(t *testing.T) {
	got, err := EncodeState(stateDraw())
	golden(t, "state-draw.golden", got, err)
	got, err = EncodeState(stateUnknown())
	golden(t, "state-unknown.golden", got, err)
}

func TestGoldenTank(t *testing.T) {
	got, err := EncodeTank(tankCooling())
	golden(t, "tank-cooling.golden", got, err)
	got, err = EncodeTank(tankUnknown())
	golden(t, "tank-unknown.golden", got, err)
}

var eventGoldens = []struct {
	name string
	ev   func() Event
}{
	{"event-draw-started.golden", drawStarted},
	{"event-draw-ended.golden", drawEnded},
	{"event-draw-balanced.golden", drawBalanced},
	{"event-draw-balanced-masked.golden", drawBalancedMasked},
	{"event-continuous-flow-started.golden", flowStarted},
	{"event-continuous-flow-ended.golden", flowEnded},
	{"event-heating-started.golden", heatingStarted},
	{"event-tank-state-changed.golden", tankStateChanged},
}

func TestGoldenEvents(t *testing.T) {
	for _, g := range eventGoldens {
		got, err := EncodeEvent(g.ev())
		golden(t, g.name, got, err)
	}
}

func TestGoldenStatus(t *testing.T) {
	got, err := EncodeStatus(statusHeartbeat())
	golden(t, "status-heartbeat.golden", got, err)
	got, err = EncodeStatus(statusShutdown())
	golden(t, "status-shutdown.golden", got, err)
	golden(t, "lwt.golden", LWT(), nil)
}

func TestTopics(t *testing.T) {
	for got, want := range map[string]string{
		StateTopic("water/hot"):            "water/hot/state",
		TankTopic("test/water/hot", right): "test/water/hot/tank/" + right,
		EventsTopic("water/hot"):           "water/hot/events",
		StatusTopic("water/hot"):           "water/hot/status",
		StateTopic("water/hot/"):           "water/hot/state",
		TankTopic("water/hot/", "a-1"):     "water/hot/tank/a-1",
	} {
		if got != want {
			t.Errorf("topic %q, want %q", got, want)
		}
	}
}

// Numbers are rounded to 3 decimals, printed without trailing zeros, and
// a rounded negative zero is 0.
func TestNumRounding(t *testing.T) {
	for x, want := range map[float64]string{
		62: "62", 0.8934999: "0.893", 0.8935001: "0.894", -1.3304: "-1.33", -0.0004: "0",
		1e-9: "0", 287.43912: "287.439", 1234567.5: "1234567.5", 0.1 + 0.2: "0.3",
	} {
		got, err := json.Marshal(Num(x))
		if err != nil || string(got) != want {
			t.Errorf("Num(%v) = %s, %v; want %s", x, got, err, want)
		}
	}
}

func TestEncodeRefusesNonFinite(t *testing.T) {
	s := stateDraw()
	s.AmbientC = n(nanValue())
	if _, err := EncodeState(s); err == nil {
		t.Error("a NaN encoded")
	}
}

func nanValue() float64 { z := 0.0; return z / z }

// Times are UTC with exactly three decimals, whatever zone they come in.
func TestTimeFormat(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	got, err := json.Marshal(Time{time.Date(2001, 1, 1, 1, 0, 5, 120_999_999, loc)})
	if err != nil || string(got) != `"2001-01-01T00:00:05.120Z"` {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	for _, s := range []State{stateDraw(), stateUnknown()} {
		raw, _ := EncodeState(s)
		got, err := DecodeState(raw)
		if err != nil {
			t.Fatalf("DecodeState: %v", err)
		}
		s.Schema = SchemaState
		if !reflect.DeepEqual(got, s) {
			t.Errorf("state round trip\n got %+v\nwant %+v", got, s)
		}
	}
	for _, x := range []Tank{tankCooling(), tankUnknown()} {
		raw, _ := EncodeTank(x)
		got, err := DecodeTank(raw)
		x.Schema = SchemaTank
		if err != nil || !reflect.DeepEqual(got, x) {
			t.Errorf("tank round trip: %v\n got %+v\nwant %+v", err, got, x)
		}
	}
	for _, g := range eventGoldens {
		e := g.ev()
		raw, _ := EncodeEvent(e)
		got, err := DecodeEvent(raw)
		e.Schema = SchemaEvent
		if e.DrawBalanced != nil && e.DrawBalanced.Reasons == nil {
			e.DrawBalanced.Reasons = []Reason{}
		}
		if err != nil || !reflect.DeepEqual(got, e) {
			t.Errorf("%s round trip: %v\n got %+v\nwant %+v", g.name, err, got, e)
		}
	}
	for _, s := range []Status{statusHeartbeat(), statusShutdown()} {
		raw, _ := EncodeStatus(s)
		got, err := DecodeStatus(raw)
		s.Schema = SchemaStatus
		if err != nil || !reflect.DeepEqual(got, s) {
			t.Errorf("status round trip: %v\n got %+v\nwant %+v", err, got, s)
		}
	}
	got, err := DecodeStatus(LWT())
	if err != nil || got.Online || got.Event != StatusLWT {
		t.Errorf("LWT decodes to %+v, %v", got, err)
	}
}

// A consumer accepts keys it doesn't know (new fields are additive).
func TestDecodeAcceptsUnknownKeys(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "state-draw.golden"))
	raw = bytes.Replace(raw, []byte(`"stored_kwh":21.79`), []byte(`"new_field":{"x":1},"stored_kwh":21.79`), 1)
	if _, err := DecodeState(raw); err != nil {
		t.Errorf("an unknown key was refused: %v", err)
	}
}

// Every key is always present: an absent one is a broken publisher, not
// an unknown value (WIRE.md, Conventions).
func TestDecodeRefuses(t *testing.T) {
	read := func(name string) string {
		x, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(x)
	}
	state, tank := read("state-draw.golden"), read("tank-cooling.golden")
	balanced, masked := read("event-draw-balanced.golden"), read("event-draw-balanced-masked.golden")
	ended := read("event-draw-ended.golden")
	for _, c := range []struct {
		name, raw string
		dec       func([]byte) error
	}{
		{"a top-level key missing", strings.Replace(state, `"ambient_c":25.7,`, "", 1), stateDec},
		{"a nested key missing", strings.Replace(state, `"hw":false,`, "", 1), stateDec},
		{"a key missing in a list element", strings.Replace(state, `"top_c":63.5`, `"x":1`, 1), stateDec},
		{"a key missing in active_draw", strings.Replace(state, `"flow_l_min":6.3,`, "", 1), stateDec},
		{"null for a value that's never unknown", strings.Replace(state, `"litres":48,`, `"litres":null,`, 1), stateDec},
		{"trailing data", state + "x", stateDec},
		{"tanks null", strings.Replace(state, `"tanks":[`, `"tanks":null,"y":[`, 1), stateDec},
		{"device_id null", strings.Replace(tank, `"device_id":"hot_water_cylinder_right"`, `"device_id":null`, 1), tankDec},
		{"the wrong schema", strings.Replace(tank, "tank.v1", "state.v1", 1), tankDec},
		{"a v2 schema", strings.Replace(tank, "tank.v1", "tank.v2", 1), tankDec},
		{"a time without milliseconds", strings.Replace(tank, `"sweep_at":"2001-01-01T00:36:32.000Z"`, `"sweep_at":"2001-01-01T00:36:32Z"`, 1), tankDec},
		{"a time with an offset", strings.Replace(tank, `"sweep_at":"2001-01-01T00:36:32.000Z"`, `"sweep_at":"2001-01-01T01:36:32.000+01:00"`, 1), tankDec},
		{"a share without litres", strings.Replace(balanced, `"litres":5.338,`, "", 1), eventDec},
		{"a null share", strings.Replace(masked, `"device_id":"hot_water_cylinder_left",`, `"device_id":"hot_water_cylinder_left","share":null,"litres":null,`, 1), eventDec},
		{"reasons null", strings.Replace(balanced, `"reasons":[]`, `"reasons":null`, 1), eventDec},
		{"no body", strings.Replace(ended, `,"draw_ended":{`, `,"other":{`, 1), eventDec},
		{"the wrong body", strings.Replace(ended, `"draw_ended":{`, `"draw_balanced":{`, 1), eventDec},
		{"two bodies", strings.Replace(ended, `"draw_ended":{`, `"active_draw":{"draw_id":"x","started_at":"2001-01-01T00:10:10.308Z","last_litre_at":"2001-01-01T00:10:10.308Z","litres":1,"flow_l_min":null,"kind":"tap"},"draw_ended":{`, 1), eventDec},
		{"not JSON", "{", stateDec},
		{"an array", "[]", stateDec},
	} {
		if err := c.dec([]byte(c.raw)); err == nil {
			t.Errorf("%s: decoded", c.name)
		}
	}
}

func stateDec(b []byte) error { _, err := DecodeState(b); return err }
func tankDec(b []byte) error  { _, err := DecodeTank(b); return err }
func eventDec(b []byte) error { _, err := DecodeEvent(b); return err }

// An event a consumer doesn't know is ErrUnknownEvent, which it ignores
// (new events are additive).
func TestDecodeUnknownEvent(t *testing.T) {
	_, err := DecodeEvent([]byte(`{"schema":"net.swee.washhouse.event.v1","event":"tank_exploded","timestamp":"2001-01-01T00:00:00.000Z","boom":{}}`))
	if !errors.Is(err, ErrUnknownEvent) {
		t.Errorf("got %v, want ErrUnknownEvent", err)
	}
}

// Encoding refuses an event whose body doesn't match its type, so a
// publisher bug can't put a malformed message on the wire.
func TestEncodeEventRefusesMismatch(t *testing.T) {
	e := drawEnded()
	e.ActiveDraw = drawStarted().ActiveDraw
	if _, err := EncodeEvent(e); err == nil {
		t.Error("two bodies encoded")
	}
	e = drawEnded()
	e.Event = EventDrawBalanced
	if _, err := EncodeEvent(e); err == nil {
		t.Error("the wrong body encoded")
	}
	e = drawBalanced()
	e.DrawBalanced.Tanks[0].Litres = nil
	if _, err := EncodeEvent(e); err == nil {
		t.Error("a share without litres encoded")
	}
}

// json.Unmarshal matches keys case-insensitively, so a key that differs
// from a known one only in case would silently override it. It is refused.
func TestDecodeRefusesACaseVariantKey(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "tank-cooling.golden"))
	bad := bytes.Replace(raw, []byte(`}`), []byte(`,"PROBES_TOTAL":9}`), 1)
	if got, err := DecodeTank(bad); err == nil {
		t.Errorf("decoded, probes_total %v", *got.ProbesTotal)
	}
	st, _ := os.ReadFile(filepath.Join("testdata", "state-draw.golden"))
	bad = bytes.Replace(st, []byte(`"hw":false`), []byte(`"hw":false,"Hw":true`), 1)
	if _, err := DecodeState(bad); err == nil {
		t.Error("a nested case variant decoded")
	}
}

// state, heat_source, volume_kind and the status event are closed: a value
// outside them is an error, both ways. kind and reasons are open (WIRE.md,
// Conventions).
func TestClosedVocabularies(t *testing.T) {
	read := func(name string) string {
		x, _ := os.ReadFile(filepath.Join("testdata", name))
		return string(x)
	}
	tank, state, ended := read("tank-cooling.golden"), read("state-draw.golden"), read("event-draw-ended.golden")
	heating, hb := read("event-heating-started.golden"), read("status-heartbeat.golden")
	for _, c := range []struct {
		name, raw string
		dec       func([]byte) error
	}{
		{"tank state", strings.Replace(tank, `"state":"cooling"`, `"state":"bogus"`, 1), tankDec},
		{"empty tank state", strings.Replace(tank, `"state":"cooling"`, `"state":""`, 1), tankDec},
		{"heat source", strings.Replace(tank, `"heat_source":null`, `"heat_source":"gas"`, 1), tankDec},
		{"summary state", strings.Replace(state, `"state":"drawing"`, `"state":"boiling"`, 1), stateDec},
		{"event prev", strings.Replace(heating, `"prev":"cooling"`, `"prev":"x"`, 1), eventDec},
		{"event heat source", strings.Replace(heating, `"heat_source":"hw"`, `"heat_source":"solar"`, 1), eventDec},
		{"volume kind", strings.Replace(ended, `"volume_kind":"metered"`, `"volume_kind":"guessed"`, 1), eventDec},
		{"empty device id", strings.Replace(tank, `"device_id":"hot_water_cylinder_right"`, `"device_id":""`, 1), tankDec},
		{"device id with a slash", strings.Replace(state, `"device_id":"hot_water_cylinder_left"`, `"device_id":"a/b"`, 1), stateDec},
		{"status event", strings.Replace(hb, `"HEARTBEAT"`, `"NAP"`, 1), statusDec},
		{"heartbeat offline", strings.Replace(hb, `"online":true`, `"online":false`, 1), statusDec},
		{"shutdown online", strings.Replace(read("status-shutdown.golden"), `"online":false`, `"online":true`, 1), statusDec},
		{"reason on a heartbeat", strings.Replace(hb, `]}`, `],"reason":"SIGTERM"}`, 1), statusDec},
		{"status tank id", strings.Replace(hb, `"tanks":["hot_water_cylinder_left"`, `"tanks":["Left/1"`, 1), statusDec},
	} {
		if err := c.dec([]byte(c.raw)); err == nil {
			t.Errorf("%s: decoded", c.name)
		}
	}
	// kind is open: a consumer maps one it doesn't know to unknown.
	if err := eventDec([]byte(strings.Replace(ended, `"kind":"shower"`, `"kind":"hot_tub"`, 1))); err != nil {
		t.Errorf("an unknown kind was refused: %v", err)
	}
	// The encoders refuse the same, so a publisher bug can't send it.
	x := tankCooling()
	x.State = "bogus"
	if _, err := EncodeTank(x); err == nil {
		t.Error("a bogus state encoded")
	}
	s := statusHeartbeat()
	s.Online = false
	if _, err := EncodeStatus(s); err == nil {
		t.Error("an offline heartbeat encoded")
	}
}

func statusDec(b []byte) error { _, err := DecodeStatus(b); return err }

// The encoders turn a nil list into [], which the decoders require: what
// one encodes, the other accepts.
func TestEncodersFillNilLists(t *testing.T) {
	raw, err := EncodeState(State{Timestamp: at("2001-01-01T00:00:00.000Z")})
	if _, derr := DecodeState(raw); err != nil || derr != nil {
		t.Errorf("state: %v, %v: %s", err, derr, raw)
	}
	raw, err = EncodeStatus(Status{Online: true, Event: StatusStartup, Timestamp: at("2001-01-01T00:00:00.000Z"), Version: "x"})
	if _, derr := DecodeStatus(raw); err != nil || derr != nil {
		t.Errorf("status: %v, %v: %s", err, derr, raw)
	}
	raw, err = EncodeEvent(Event{Event: EventDrawBalanced, Timestamp: at("2001-01-01T00:00:00.000Z"),
		DrawBalanced: &DrawBalanced{DrawID: "x-1", EndedAt: at("2001-01-01T00:00:00.000Z")}})
	if _, derr := DecodeEvent(raw); err != nil || derr != nil {
		t.Errorf("event: %v, %v: %s", err, derr, raw)
	}
}

// Every golden is pinned by a test above: one nothing checks would be a
// contract nobody holds to.
func TestEveryGoldenIsUsed(t *testing.T) {
	used := map[string]bool{"state-draw.golden": true, "state-unknown.golden": true, "tank-cooling.golden": true,
		"tank-unknown.golden": true, "status-heartbeat.golden": true, "status-shutdown.golden": true, "lwt.golden": true}
	for _, g := range eventGoldens {
		used[g.name] = true
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "*.golden"))
	if len(files) != len(used) {
		t.Errorf("%d goldens, %d pinned", len(files), len(used))
	}
	for _, f := range files {
		if !used[filepath.Base(f)] {
			t.Errorf("%s is pinned by no test", f)
		}
	}
}
