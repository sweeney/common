package devices

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func f64(v float64) *float64             { return &v }
func dur(v time.Duration) *time.Duration { return &v }

// The fixture has the shape of a live devices_<site> document: every key the
// namespace holds today, legacy and canonical identities, and a device with no
// room or floor at all (as one live site's devices have). It is made up; the live
// documents describe a real house and stay out of this public repository.
func TestDecodeFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/devices_site.json")
	if err != nil {
		t.Fatal(err)
	}
	got, problems, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}
	want := map[string]Device{
		"kitchen-dishwasher": {
			Scheme: "zigbee", Primary: "0x00000000000000a1",
			IEEEAddress: "0x00000000000000a1",
			Class:       "cycle_power_device", DisplayName: "Dishwasher (Kitchen)",
			Room: "ground.kitchen", Floor: "ground",
			Coordinate:     &Coordinate{8.241, 3.211},
			EnergyStrategy: "integration",
			Thresholds: &Thresholds{
				IdleBelowW: f64(5), ActiveAboveW: f64(50),
				ActiveSustainedFor: dur(30 * time.Second), InactiveSustainedFor: dur(30 * time.Minute),
			},
		},
		"hall-climate": {
			Scheme: "zigbee", Primary: "0x00000000000000a2", Display: "hall_climate",
			IEEEAddress: "0x00000000000000a2", FriendlyName: "hall_climate",
			Class: "climate_sensor", DisplayName: "Climate: Hall",
			Room: "ground.hall", Floor: "ground",
			Coordinate:        &Coordinate{1.5, 2},
			Note:              "ceiling",
			EnvironmentFields: []string{"humidity_pct", "temperature_c"},
		},
		"freezer-probe": {
			Scheme: "climate", Primary: "freezer",
			Class: "temperature_probe", DisplayName: "Freezer",
			Room: "ground.kitchen", Floor: "ground",
			Coordinate:        &Coordinate{9, 4.25},
			Thresholds:        &Thresholds{ActiveSustainedFor: dur(0)},
			TargetTemperature: f64(-18),
			EnvironmentFields: []string{"temperature_c"},
		},
		"electricity-meter": {
			Scheme: "meter", Primary: "meter",
			Class: "energy_meter", DisplayName: "Electricity meter",
			Room: "ground.hall", Floor: "ground", Covers: "house",
			Coordinate: &Coordinate{0.5, 0.5},
		},
		"site-weatherstation": {
			Scheme: "climate", Primary: "site",
			Class: "environmental_sensor", DisplayName: "Climate: Weather Station",
			EnvironmentFields: []string{"pressure_hpa", "temperature_c", "wind_speed_ms"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		for id := range want {
			if !reflect.DeepEqual(got[id], want[id]) {
				t.Errorf("%s:\n got %+v\nwant %+v", id, got[id], want[id])
			}
		}
		if len(got) != len(want) {
			t.Errorf("got %d devices, want %d", len(got), len(want))
		}
	}
	if p := Check(got); len(p) != 0 {
		t.Errorf("Check = %v, want none", p)
	}
}

// One bad value costs that field, never the device or the document.
func TestDecodeDropsOnlyTheBadField(t *testing.T) {
	cases := map[string]struct {
		key, value string
	}{
		"string target_temperature":   {"target_temperature", `"5"`},
		"bad duration in thresholds":  {"thresholds", `{"idle_below_w": 5, "active_sustained_for": "soon"}`},
		"non-number threshold":        {"thresholds", `{"idle_below_w": "five"}`},
		"thresholds not an object":    {"thresholds", `[]`},
		"environment_fields a string": {"environment_fields", `"temperature_c"`},
		"coordinate of three":         {"coordinate", `[1, 2, 3]`},
		"coordinate of one":           {"coordinate", `[1]`},
		"room a number":               {"room", `7`},
		"class an object":             {"class", `{}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			doc := `{
				"good": {"class": "energy_meter", "scheme": "meter", "primary": "m"},
				"bad": {"class": "climate_sensor", "display_name": "Bad", "scheme": "climate",
				        "primary": "p", "floor": "ground", "` + c.key + `": ` + c.value + `}
			}`
			got, problems, err := Decode([]byte(doc))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(got) != 2 {
				t.Fatalf("got %d devices, want 2", len(got))
			}
			if len(problems) != 1 {
				t.Fatalf("problems = %v, want one", problems)
			}
			p := problems[0]
			if p.ID != "bad" || p.Field != c.key || p.Code != CodeBadValue {
				t.Errorf("problem = %+v, want bad/%s/%s", p, c.key, CodeBadValue)
			}
			bad := got["bad"]
			if bad.DisplayName != "Bad" || bad.Floor != "ground" || bad.Primary != "p" {
				t.Errorf("the rest of the device was lost: %+v", bad)
			}
			// The field itself is left unset: no partial value.
			if v := fieldByKey(t, bad, c.key); !v.IsZero() {
				t.Errorf("%s = %v, want it dropped", c.key, v.Interface())
			}
		})
	}
}

func fieldByKey(t *testing.T, d Device, key string) reflect.Value {
	t.Helper()
	v := reflect.ValueOf(d)
	for i := 0; i < v.NumField(); i++ {
		if name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ","); name == key {
			return v.Field(i)
		}
	}
	t.Fatalf("no field for %q", key)
	return reflect.Value{}
}

func TestDecodeDeviceNotAnObject(t *testing.T) {
	got, problems, err := Decode([]byte(`{"a": {"class": "x"}, "b": "nope", "c": null}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["a"]; !ok || len(got) != 1 {
		t.Errorf("got %v, want only a", got)
	}
	if len(problems) != 2 || problems[0].ID != "b" || problems[1].ID != "c" ||
		problems[0].Field != "" || problems[0].Code != CodeBadValue {
		t.Errorf("problems = %+v, want b and c, whole-device", problems)
	}
}

func TestDecodeRefusesANonObjectDocument(t *testing.T) {
	for _, doc := range []string{`[]`, `"x"`, `null`, `7`, ``, `{`} {
		if _, _, err := Decode([]byte(doc)); err == nil {
			t.Errorf("Decode(%q) = nil error, want one", doc)
		}
	}
}

// Unknown keys are ignored, so the namespace can gain a key before this
// module does. Keys match case-insensitively, as encoding/json does, so the
// services keep reading what they read before.
func TestDecodeKeys(t *testing.T) {
	got, problems, err := Decode([]byte(`{"d": {"Room": "ground.hall", "future_key": [1], "class": "x"}}`))
	if err != nil || len(problems) != 0 {
		t.Fatalf("err %v, problems %v", err, problems)
	}
	if got["d"].Room != "ground.hall" || got["d"].Class != "x" {
		t.Errorf("got %+v", got["d"])
	}
}

func TestDecodeNullIsAbsent(t *testing.T) {
	got, problems, err := Decode([]byte(`{"d": {"room": null, "thresholds": null, "coordinate": null}}`))
	if err != nil || len(problems) != 0 {
		t.Fatalf("err %v, problems %v", err, problems)
	}
	if !reflect.DeepEqual(got["d"], Device{}) {
		t.Errorf("got %+v, want zero", got["d"])
	}
}

// Problems come back in a stable order: by device id, then key.
func TestDecodeProblemOrder(t *testing.T) {
	_, problems, _ := Decode([]byte(`{
		"z": {"room": 1},
		"a": {"room": 1, "floor": 2, "class": 3}
	}`))
	var got []string
	for _, p := range problems {
		got = append(got, p.ID+"/"+p.Field)
	}
	want := []string{"a/class", "a/floor", "a/room", "z/room"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestThresholdsJSONRoundTrip(t *testing.T) {
	in := Thresholds{IdleBelowW: f64(0), ActiveSustainedFor: dur(90 * time.Second), InactiveSustainedFor: dur(0)}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"idle_below_w":0,"active_sustained_for":"1m30s","inactive_sustained_for":"0s"}`; string(b) != want {
		t.Errorf("Marshal = %s, want %s", b, want)
	}
	var out Thresholds
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
}

func TestDeviceJSONRoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/devices_site.json")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := Decode(data)
	b, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	second, problems, err := Decode(b)
	if err != nil || len(problems) != 0 {
		t.Fatalf("err %v, problems %v", err, problems)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("round trip changed the document")
	}
}

func TestNormalise(t *testing.T) {
	cases := map[string]struct{ in, want Device }{
		"ieee and friendly fold in": {
			Device{IEEEAddress: "0x1", FriendlyName: "lamp"},
			Device{Scheme: "zigbee", Primary: "0x1", Display: "lamp", IEEEAddress: "0x1", FriendlyName: "lamp"},
		},
		"friendly only": {
			Device{FriendlyName: "lamp"},
			Device{Scheme: "zigbee", Display: "lamp", FriendlyName: "lamp"},
		},
		"canonical fields win": {
			Device{Scheme: "tasmota", Primary: "p", Display: "d", IEEEAddress: "0x1", FriendlyName: "lamp"},
			Device{Scheme: "tasmota", Primary: "p", Display: "d", IEEEAddress: "0x1", FriendlyName: "lamp"},
		},
		"nothing legacy": {
			Device{Scheme: "ups", Primary: "u"},
			Device{Scheme: "ups", Primary: "u"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := c.in
			d.Normalise()
			if !reflect.DeepEqual(d, c.want) {
				t.Errorf("got %+v, want %+v", d, c.want)
			}
			d.Normalise()
			if !reflect.DeepEqual(d, c.want) {
				t.Errorf("not idempotent: %+v", d)
			}
		})
	}
}

func TestPlaceAndCoverage(t *testing.T) {
	cases := map[string]struct {
		d               Device
		place, coverage string
		coversWholeSite bool
	}{
		"room":                    {Device{Room: "ground.hall"}, "ground.hall", "", false},
		"covers house":            {Device{Room: "ground.hall", Covers: "house"}, "ground.hall", "house", true},
		"covers another room":     {Device{Room: "ground.hall", Covers: "ground.kitchen"}, "ground.hall", "ground.kitchen", false},
		"legacy location":         {Device{Location: "ground.hall"}, "ground.hall", "", false},
		"legacy location house":   {Device{Location: "house"}, "", "house", true},
		"room wins over location": {Device{Room: "ground.hall", Location: "ground.kitchen"}, "ground.hall", "", false},
		// statehouse's "room published first": a namespace mid-migration may
		// publish room before covers, and the coverage fact must survive it.
		"room published first":         {Device{Room: "ground.hall", Location: "house"}, "ground.hall", "house", true},
		"covers beats legacy location": {Device{Covers: "ground.kitchen", Location: "house"}, "", "ground.kitchen", false},
		"nothing":                      {Device{}, "", "", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := c.d.Place(); got != c.place {
				t.Errorf("Place = %q, want %q", got, c.place)
			}
			if got := c.d.Coverage(); got != c.coverage {
				t.Errorf("Coverage = %q, want %q", got, c.coverage)
			}
			if got := c.d.CoversWholeSite(); got != c.coversWholeSite {
				t.Errorf("CoversWholeSite = %v, want %v", got, c.coversWholeSite)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	cases := map[string]struct {
		m    map[string]Device
		want []string // ID/Code
	}{
		"clean": {map[string]Device{
			"a": {Scheme: "meter", Primary: "m", Room: "ground.hall", Floor: "ground", Covers: "house"},
		}, nil},
		"no room or floor is not a problem": {map[string]Device{
			"a": {Scheme: "ups", Primary: "u"},
		}, nil},
		"floor mismatch": {map[string]Device{
			"a": {Scheme: "meter", Primary: "m", Room: "first.hall", Floor: "ground"},
		}, []string{"a/" + CodeFloorMismatch}},
		"covers unresolved": {map[string]Device{
			"a": {Scheme: "meter", Primary: "m", Covers: "everywhere"},
		}, []string{"a/" + CodeCoversUnresolved}},
		"covers another room": {map[string]Device{
			"a": {Scheme: "meter", Primary: "m", Covers: "ground.kitchen"},
		}, nil},
		"identity missing": {map[string]Device{
			"a": {Class: "x"},
			"b": {Scheme: "zigbee"},
		}, []string{"a/" + CodeIdentityMissing, "b/" + CodeIdentityMissing}},
		"display is enough": {map[string]Device{
			"a": {Scheme: "zigbee", Display: "lamp"},
		}, nil},
		// Check folds legacy fields itself, so an un-normalised map (say,
		// statehouse's local YAML before Normalise) reads the same.
		"legacy identity counts": {map[string]Device{
			"a": {IEEEAddress: "0x1"},
		}, nil},
		"duplicate primary, case-insensitive": {map[string]Device{
			"a": {Scheme: "zigbee", Primary: "0xAB"},
			"b": {IEEEAddress: "0xab"},
		}, []string{"b/" + CodeDuplicateIdentity}},
		"duplicate display": {map[string]Device{
			"a": {Scheme: "zigbee", Primary: "1", Display: "Lamp"},
			"b": {Scheme: "zigbee", Primary: "2", FriendlyName: "lamp"},
		}, []string{"b/" + CodeDuplicateIdentity}},
		"same name, different scheme": {map[string]Device{
			"a": {Scheme: "zigbee", Primary: "x"},
			"b": {Scheme: "tasmota", Primary: "x"},
		}, nil},
		"legacy location": {map[string]Device{
			"a": {Scheme: "meter", Primary: "m", Location: "house"},
		}, []string{"a/" + CodeLegacyLocation}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var got []string
			for _, p := range Check(c.m) {
				got = append(got, p.ID+"/"+p.Code)
				if p.Message == "" {
					t.Errorf("%+v has no message", p)
				}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Check = %v, want %v", got, c.want)
			}
		})
	}
}

// Check reads; it never changes the map it is given.
func TestCheckDoesNotNormaliseTheCallersMap(t *testing.T) {
	m := map[string]Device{"a": {IEEEAddress: "0x1"}}
	Check(m)
	if m["a"].Scheme != "" {
		t.Errorf("Check changed the map: %+v", m["a"])
	}
}
