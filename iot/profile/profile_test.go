package profile

import "testing"

func sampleProfile() SensorProfile {
	return SensorProfile{
		Channels: []Channel{
			{Key: "a", Label: "Alpha"},
			{Key: "b", Label: "Bravo"},
		},
		PayloadRoot: "sensor",
	}
}

func TestKeysPreservesOrder(t *testing.T) {
	keys := sampleProfile().Keys()
	want := []string{"a", "b"}
	if len(keys) != len(want) {
		t.Fatalf("Keys length: got %d, want %d", len(keys), len(want))
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("Keys[%d]: got %q, want %q", i, keys[i], want[i])
		}
	}
}

func TestChannelLookup(t *testing.T) {
	p := sampleProfile()

	c, ok := p.Channel("b")
	if !ok {
		t.Fatal("expected to find channel b")
	}
	if c.Label != "Bravo" {
		t.Errorf("Label: got %q, want Bravo", c.Label)
	}

	if _, ok := p.Channel("missing"); ok {
		t.Error("expected missing channel to report not found")
	}
}
