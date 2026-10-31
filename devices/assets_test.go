package devices

import (
	"encoding/json"
	"strings"
	"testing"
)

// A made-up site: a hot water cylinder with two probes, a fridge with a
// probe and a plug, and the cases Check must catch.
const assetsDoc = `{
  "hot-water-cylinder": {
    "class": "hot_water_cylinder", "display_name": "Hot water cylinder",
    "telemetry": false, "room": "first.cupboard", "floor": "first",
    "manufacturer": "Example Co", "model": "UV-210", "installed": "2019-05-14",
    "cylinder": {"capacity_l": 210, "height_mm": 1480, "diameter_mm": 550}
  },
  "hot-water-cylinder-top": {
    "class": "appliance_probe", "scheme": "tanktemp", "primary": "hot-water-cylinder.top",
    "environment_fields": ["temperature_c"], "part_of": "hot-water-cylinder"
  },
  "hot-water-cylinder-bottom": {
    "class": "appliance_probe", "scheme": "tanktemp", "primary": "hot-water-cylinder.bottom",
    "part_of": "hot-water-cylinder", "target_temperature": 45
  },
  "fridge": {"class": "fridge", "telemetry": false, "room": "ground.kitchen", "floor": "ground"},
  "fridge-probe": {"class": "appliance_probe", "scheme": "climate", "primary": "fridge",
    "room": "ground.kitchen", "part_of": "fridge", "target_temperature": 4},
  "fridge-plug": {"class": "cycle_power_device", "ieee_address": "0x00000000000000f1", "part_of": "fridge"}
}`

func decodeOK(t *testing.T, doc string) map[string]Device {
	t.Helper()
	m, problems, err := Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("decode: %s", p)
	}
	return m
}

func TestAssetFieldsDecode(t *testing.T) {
	m := decodeOK(t, assetsDoc)
	c := m["hot-water-cylinder"]
	if c.Manufacturer != "Example Co" || c.Model != "UV-210" || c.Installed != "2019-05-14" {
		t.Errorf("asset fields = %+v", c)
	}
	if c.Telemetry == nil || *c.Telemetry {
		t.Errorf("telemetry = %v, want an explicit false", c.Telemetry)
	}
	if c.Cylinder == nil || *c.Cylinder.CapacityL != 210 || *c.Cylinder.HeightMM != 1480 || *c.Cylinder.DiameterMM != 550 {
		t.Errorf("cylinder = %+v", c.Cylinder)
	}
	if p := m["hot-water-cylinder-top"]; p.PartOf != "hot-water-cylinder" || p.Telemetry != nil {
		t.Errorf("probe = %+v", p)
	}
}

func TestSendsTelemetry(t *testing.T) {
	m := decodeOK(t, assetsDoc)
	if m["hot-water-cylinder"].SendsTelemetry() || m["fridge"].SendsTelemetry() {
		t.Error("telemetry: false entries send nothing")
	}
	if !m["hot-water-cylinder-top"].SendsTelemetry() || !m["fridge-plug"].SendsTelemetry() {
		t.Error("an absent telemetry key means true")
	}
	on := true
	if !(Device{Telemetry: &on}).SendsTelemetry() {
		t.Error("explicit true")
	}
}

func TestPlaceOfResolvesPartOfOneLevel(t *testing.T) {
	m := decodeOK(t, assetsDoc)
	tests := map[string]string{
		"hot-water-cylinder-top":    "first.cupboard", // from the cylinder
		"hot-water-cylinder-bottom": "first.cupboard",
		"fridge-probe":              "ground.kitchen", // its own room wins
		"fridge-plug":               "ground.kitchen", // from the fridge
		"hot-water-cylinder":        "first.cupboard",
		"no-such-device":            "",
	}
	for id, want := range tests {
		if got := PlaceOf(m, id); got != want {
			t.Errorf("PlaceOf(%s) = %q, want %q", id, got, want)
		}
	}
	// One level only: a chain doesn't resolve further.
	chain := map[string]Device{
		"a": {Room: "first.room-a"},
		"b": {PartOf: "a"},
		"c": {PartOf: "b"},
	}
	if got := PlaceOf(chain, "c"); got != "" {
		t.Errorf("chain resolved to %q; part_of is one level", got)
	}
	// Place() itself is unchanged: no map, no part_of.
	if got := m["hot-water-cylinder-top"].Place(); got != "" {
		t.Errorf("Place() = %q; it must not change meaning", got)
	}
}

func TestAssetValuesDecodeFieldByField(t *testing.T) {
	m, problems, err := Decode([]byte(`{
	  "c": {"class": "hot_water_cylinder", "telemetry": "no", "installed": "14/05/2019",
	        "cylinder": {"capacity_l": "big", "height_mm": 1480}},
	  "d": {"cylinder": [1, 2]},
	  "e": {"installed": "2019-02-30"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	c := m["c"]
	if c.Class != "hot_water_cylinder" || c.Telemetry != nil || c.Installed != "" {
		t.Errorf("c = %+v", c)
	}
	if c.Cylinder == nil || c.Cylinder.CapacityL != nil || *c.Cylinder.HeightMM != 1480 {
		t.Errorf("a bad capacity loses only itself: %+v", c.Cylinder)
	}
	if m["d"].Cylinder != nil {
		t.Error("a cylinder that isn't an object is dropped")
	}
	if m["e"].Installed != "" {
		t.Error("an impossible date is dropped")
	}
	want := []string{"c.cylinder.capacity_l", "c.installed", "c.telemetry", "d.cylinder", "e.installed"}
	if len(problems) != len(want) {
		t.Fatalf("problems = %v", problems)
	}
	for i, w := range want {
		if got := problems[i].ID + "." + problems[i].Field; got != w || problems[i].Code != CodeBadValue {
			t.Errorf("problem %d = %s, want %s bad_value", i, problems[i], w)
		}
	}
}

func TestCheckAssets(t *testing.T) {
	m := decodeOK(t, assetsDoc)
	if problems := Check(m); len(problems) != 0 {
		t.Errorf("a clean document: %v", problems)
	}

	bad := decodeOK(t, `{
	  "tank": {"class": "hot_water_cylinder", "telemetry": false, "room": "first.cupboard"},
	  "noisy-tank": {"class": "hot_water_cylinder", "telemetry": false, "scheme": "tanktemp", "primary": "x"},
	  "zigbee-tank": {"telemetry": false, "ieee_address": "0x00000000000000aa"},
	  "orphan": {"scheme": "tanktemp", "primary": "a.top", "part_of": "nowhere"},
	  "grandchild": {"scheme": "tanktemp", "primary": "a.b", "part_of": "probe"},
	  "probe": {"scheme": "tanktemp", "primary": "a.mid", "part_of": "tank"},
	  "elsewhere": {"scheme": "tanktemp", "primary": "a.c", "part_of": "tank", "room": "ground.hall"},
	  "self": {"scheme": "tanktemp", "primary": "a.d", "part_of": "self"},
	  "forgot": {"class": "hot_water_cylinder", "room": "first.cupboard"}
	}`)
	got := map[string]string{}
	for _, p := range Check(bad) {
		got[p.ID] += p.Code + " "
	}
	want := map[string]string{
		"noisy-tank":  CodeTelemetryContradiction,
		"zigbee-tank": CodeTelemetryContradiction,
		"orphan":      CodePartOfUnresolved,
		"grandchild":  CodePartOfChain,
		"elsewhere":   CodePartOfRoomMismatch,
		"self":        CodePartOfChain,
		"forgot":      CodeIdentityMissing, // forgot telemetry: false: the existing warning
	}
	for id, code := range want {
		if strings.TrimSpace(got[id]) != code {
			t.Errorf("%s: got %q, want %s", id, got[id], code)
		}
	}
	for _, id := range []string{"tank", "probe"} {
		if got[id] != "" {
			t.Errorf("%s: unexpected %s", id, got[id])
		}
	}
}

func TestAssetFieldsRoundTrip(t *testing.T) {
	m := decodeOK(t, assetsDoc)
	b, err := json.Marshal(m["hot-water-cylinder"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"telemetry":false`, `"installed":"2019-05-14"`, `"cylinder":{"capacity_l":210,"height_mm":1480,"diameter_mm":550}`, `"manufacturer":"Example Co"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %s", b, want)
		}
	}
	b, _ = json.Marshal(m["hot-water-cylinder-top"])
	if strings.Contains(string(b), `"telemetry":`) || strings.Contains(string(b), `"cylinder":`) {
		t.Errorf("absent keys stay absent: %s", b)
	}
}
