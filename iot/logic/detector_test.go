package logic

import (
	"testing"
	"time"

	"github.com/sweeney/common/iot/profile"
)

// twoChannel is a generic two-channel profile (keys a/b) used to exercise
// multi-channel behaviour (baseline, simultaneous transitions, counts) without
// any sensor-specific vocabulary.
func twoChannel() profile.SensorProfile {
	return profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "a", OnEvent: "A_ON", OffEvent: "A_OFF"},
			{Key: "b", OnEvent: "B_ON", OffEvent: "B_OFF"},
		},
	}
}

// pressProfile is a one-channel momentary-press profile with a short per-channel
// debounce, used to verify per-channel debounce overrides.
var pressProfile = profile.SensorProfile{
	Channels: []profile.Channel{
		{Key: "button", Label: "Button", OnEvent: "PRESS", OffEvent: "RELEASE", Debounce: 50 * time.Millisecond},
	},
}

func vals(a, b bool) map[string]bool { return map[string]bool{"a": a, "b": b} }

// setupBaselined creates a two-channel detector that has already baselined to
// the given states.
func setupBaselined(t *testing.T, aOn, bOn bool) *Detector {
	t.Helper()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, now)
	d.ProcessValues(vals(aOn, bOn), now)
	d.ProcessValues(vals(aOn, bOn), now.Add(250*time.Millisecond))
	if !d.IsBaselined() {
		t.Fatal("failed to establish baseline")
	}
	return d
}

func TestNewProfileDetector(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, startTime)
	if d == nil {
		t.Fatal("NewProfileDetector returned nil")
	}
	if d.debounceDuration != 250*time.Millisecond {
		t.Errorf("expected debounce duration 250ms, got %v", d.debounceDuration)
	}
	if d.baselined {
		t.Error("new detector should not be baselined")
	}
	if !d.startTime.Equal(startTime) {
		t.Errorf("expected startTime %v, got %v", startTime, d.startTime)
	}
	if !d.lastHeartbeat.Equal(startTime) {
		t.Errorf("expected lastHeartbeat %v, got %v", startTime, d.lastHeartbeat)
	}
}

func TestBaselineEstablishment(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, now)

	if got := d.ProcessValues(vals(true, false), now); len(got) != 0 {
		t.Errorf("expected no events during baseline, got %d", len(got))
	}
	if d.IsBaselined() {
		t.Error("should not be baselined after first sample")
	}

	if got := d.ProcessValues(vals(true, false), now.Add(200*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events during baseline, got %d", len(got))
	}
	if d.IsBaselined() {
		t.Error("should not be baselined before debounce period")
	}

	if got := d.ProcessValues(vals(true, false), now.Add(250*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events at baseline establishment, got %d", len(got))
	}
	if !d.IsBaselined() {
		t.Error("should be baselined after debounce period")
	}

	states := d.CurrentStates()
	if states["a"] != StateOn {
		t.Errorf("expected a=ON, got %s", states["a"])
	}
	if states["b"] != StateOff {
		t.Errorf("expected b=OFF, got %s", states["b"])
	}
}

func TestBaselineResetOnChange(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, now)

	d.ProcessValues(vals(true, false), now)
	d.ProcessValues(vals(false, false), now.Add(100*time.Millisecond))

	if got := d.ProcessValues(vals(false, false), now.Add(250*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events, got %d", len(got))
	}

	if got := d.ProcessValues(vals(false, false), now.Add(350*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events at baseline establishment, got %d", len(got))
	}
	if !d.IsBaselined() {
		t.Error("should be baselined after debounce from state change")
	}
	if d.CurrentStates()["a"] != StateOff {
		t.Errorf("expected a=OFF, got %s", d.CurrentStates()["a"])
	}
}

func TestNoEventsForStableState(t *testing.T) {
	d := setupBaselined(t, true, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		events := d.ProcessValues(vals(true, false), now.Add(time.Duration(i)*100*time.Millisecond))
		if len(events) != 0 {
			t.Errorf("iteration %d: expected no events for stable state, got %d", i, len(events))
		}
	}
}

func TestSingleTransitionOnToOff(t *testing.T) {
	d := setupBaselined(t, true, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	if got := d.ProcessValues(vals(false, false), now); len(got) != 0 {
		t.Errorf("expected no events before debounce, got %d", len(got))
	}
	if got := d.ProcessValues(vals(false, false), now.Add(200*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events before debounce, got %d", len(got))
	}

	events := d.ProcessValues(vals(false, false), now.Add(250*time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected 1 event after debounce, got %d", len(events))
	}
	e := events[0]
	if e.Name != "A_OFF" {
		t.Errorf("expected A_OFF event, got %s", e.Name)
	}
	if e.States["a"] != StateOff {
		t.Errorf("expected a=OFF, got %s", e.States["a"])
	}
	if e.States["b"] != StateOff {
		t.Errorf("expected b=OFF, got %s", e.States["b"])
	}
	if !e.Timestamp.Equal(now.Add(250 * time.Millisecond)) {
		t.Errorf("unexpected timestamp: %v", e.Timestamp)
	}
}

func TestSingleTransitionOffToOn(t *testing.T) {
	d := setupBaselined(t, false, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	d.ProcessValues(vals(true, false), now)
	events := d.ProcessValues(vals(true, false), now.Add(250*time.Millisecond))
	if len(events) != 1 || events[0].Name != "A_ON" {
		t.Fatalf("expected A_ON event, got %v", events)
	}
	if events[0].States["a"] != StateOn {
		t.Errorf("expected a=ON, got %s", events[0].States["a"])
	}
}

func TestBounceShorterThanDebounce(t *testing.T) {
	d := setupBaselined(t, true, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	if got := d.ProcessValues(vals(false, false), now); len(got) != 0 {
		t.Errorf("expected no events, got %d", len(got))
	}
	if got := d.ProcessValues(vals(true, false), now.Add(100*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events, got %d", len(got))
	}
	if got := d.ProcessValues(vals(true, false), now.Add(300*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events after bounce, got %d", len(got))
	}
	if d.CurrentStates()["a"] != StateOn {
		t.Errorf("expected a=ON after bounce, got %s", d.CurrentStates()["a"])
	}
}

func TestMultipleBounces(t *testing.T) {
	d := setupBaselined(t, false, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	states := []bool{true, false, true, false, true}
	for i, state := range states {
		if got := d.ProcessValues(vals(state, false), now.Add(time.Duration(i*50)*time.Millisecond)); len(got) != 0 {
			t.Errorf("iteration %d: expected no events during bouncing, got %d", i, len(got))
		}
	}
	if got := d.ProcessValues(vals(true, false), now.Add(250*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events (debounce timer reset), got %d", len(got))
	}
	events := d.ProcessValues(vals(true, false), now.Add(450*time.Millisecond))
	if len(events) != 1 || events[0].Name != "A_ON" {
		t.Fatalf("expected A_ON after settling, got %v", events)
	}
}

func TestBackToBackTransitions(t *testing.T) {
	d := setupBaselined(t, false, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	d.ProcessValues(vals(true, false), now)
	events := d.ProcessValues(vals(true, false), now.Add(250*time.Millisecond))
	if len(events) != 1 || events[0].Name != "A_ON" {
		t.Fatalf("expected A_ON event, got %v", events)
	}

	t2 := now.Add(300 * time.Millisecond)
	d.ProcessValues(vals(false, false), t2)
	events = d.ProcessValues(vals(false, false), t2.Add(250*time.Millisecond))
	if len(events) != 1 || events[0].Name != "A_OFF" {
		t.Fatalf("expected A_OFF event, got %v", events)
	}
}

func TestSimultaneousTransitions(t *testing.T) {
	d := setupBaselined(t, false, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	d.ProcessValues(vals(true, true), now)
	events := d.ProcessValues(vals(true, true), now.Add(250*time.Millisecond))

	if len(events) != 2 {
		t.Fatalf("expected 2 events for simultaneous transitions, got %d", len(events))
	}
	if events[0].Name != "A_ON" {
		t.Errorf("expected first event A_ON, got %s", events[0].Name)
	}
	if events[1].Name != "B_ON" {
		t.Errorf("expected second event B_ON, got %s", events[1].Name)
	}
	for i, e := range events {
		if e.States["a"] != StateOn || e.States["b"] != StateOn {
			t.Errorf("event %d: expected both states ON, got a=%s b=%s", i, e.States["a"], e.States["b"])
		}
	}
}

func TestEventContainsCorrectStates(t *testing.T) {
	d := setupBaselined(t, true, true)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	d.ProcessValues(vals(false, true), now)
	events := d.ProcessValues(vals(false, true), now.Add(250*time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].States["a"] != StateOff {
		t.Errorf("expected a=OFF, got %s", events[0].States["a"])
	}
	if events[0].States["b"] != StateOn {
		t.Errorf("expected b=ON (unchanged), got %s", events[0].States["b"])
	}
}

func TestBoolToState(t *testing.T) {
	if boolToState(true) != StateOn {
		t.Error("boolToState(true) should be ON")
	}
	if boolToState(false) != StateOff {
		t.Error("boolToState(false) should be OFF")
	}
}

func TestCurrentStatesBeforeBaseline(t *testing.T) {
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, time.Now())
	states := d.CurrentStates()
	if states["a"] != "" || states["b"] != "" {
		t.Errorf("expected empty states before baseline, got %v", states)
	}
}

func TestAsymmetricBaseline(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, now)

	d.ProcessValues(vals(true, false), now)
	d.ProcessValues(vals(true, true), now.Add(100*time.Millisecond))

	if got := d.ProcessValues(vals(true, true), now.Add(250*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events, got %d", len(got))
	}
	if d.IsBaselined() {
		t.Error("should not be fully baselined yet (b timer was reset)")
	}

	if got := d.ProcessValues(vals(true, true), now.Add(350*time.Millisecond)); len(got) != 0 {
		t.Errorf("expected no events at baseline, got %d", len(got))
	}
	if !d.IsBaselined() {
		t.Error("should be fully baselined now")
	}
}

func TestDebounceExactTiming(t *testing.T) {
	d := setupBaselined(t, false, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	d.ProcessValues(vals(true, false), now)
	if got := d.ProcessValues(vals(true, false), now.Add(249*time.Millisecond)); len(got) != 0 {
		t.Error("should not trigger at 249ms")
	}
	if got := d.ProcessValues(vals(true, false), now.Add(250*time.Millisecond)); len(got) != 1 {
		t.Error("should trigger at exactly 250ms")
	}
}

func TestStartTimeMatchesConstructorArg(t *testing.T) {
	startTime := time.Date(2026, 2, 10, 14, 30, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, startTime)
	if !d.StartTime().Equal(startTime) {
		t.Errorf("StartTime: got %v, want %v", d.StartTime(), startTime)
	}
}

func TestGenericCountsIncrementOnTransition(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, startTime)

	d.ProcessValues(vals(false, false), startTime)
	d.ProcessValues(vals(false, false), startTime.Add(250*time.Millisecond))

	if c := d.GenericCountsSnapshot(); len(c) != 0 {
		t.Errorf("expected empty counts after baseline, got %v", c)
	}

	t1 := startTime.Add(500 * time.Millisecond)
	d.ProcessValues(vals(true, false), t1)
	d.ProcessValues(vals(true, false), t1.Add(250*time.Millisecond))
	if d.GenericCountsSnapshot()["A_ON"] != 1 {
		t.Errorf("expected A_ON=1, got %d", d.GenericCountsSnapshot()["A_ON"])
	}

	t2 := t1.Add(500 * time.Millisecond)
	d.ProcessValues(vals(true, true), t2)
	d.ProcessValues(vals(true, true), t2.Add(250*time.Millisecond))
	if d.GenericCountsSnapshot()["B_ON"] != 1 {
		t.Errorf("expected B_ON=1, got %d", d.GenericCountsSnapshot()["B_ON"])
	}
}

func TestGenericCountsSnapshotIsCopy(t *testing.T) {
	d := setupBaselined(t, false, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	d.ProcessValues(vals(true, false), now)
	d.ProcessValues(vals(true, false), now.Add(250*time.Millisecond))

	snap := d.GenericCountsSnapshot()
	snap["A_ON"] = 99
	if d.GenericCountsSnapshot()["A_ON"] != 1 {
		t.Error("GenericCountsSnapshot should return an independent copy")
	}
}

func TestPerChannelDebounceOverride(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(pressProfile, 250*time.Millisecond, start)

	// Channel uses its own 50ms debounce, not the 250ms default.
	if got := d.ProcessValues(map[string]bool{"button": false}, start); len(got) != 0 {
		t.Fatalf("expected no events during baseline, got %d", len(got))
	}
	if got := d.ProcessValues(map[string]bool{"button": false}, start.Add(50*time.Millisecond)); len(got) != 0 {
		t.Fatalf("expected no events at baseline establishment, got %d", len(got))
	}
	if !d.IsBaselined() {
		t.Fatal("expected baselined after per-channel debounce")
	}

	press := start.Add(200 * time.Millisecond)
	if got := d.ProcessValues(map[string]bool{"button": true}, press); len(got) != 0 {
		t.Fatalf("expected no event before debounce, got %d", len(got))
	}
	events := d.ProcessValues(map[string]bool{"button": true}, press.Add(50*time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected 1 PRESS event, got %d", len(events))
	}
	if events[0].Key != "button" || events[0].Name != "PRESS" {
		t.Errorf("unexpected event: key=%q name=%q", events[0].Key, events[0].Name)
	}
}

func TestPerChannelDebounceDefaults(t *testing.T) {
	// A channel with no Debounce set should inherit the profile default.
	p := profile.SensorProfile{
		Channels: []profile.Channel{{Key: "x", OnEvent: "X_ON", OffEvent: "X_OFF"}},
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(p, 250*time.Millisecond, start)

	d.ProcessValues(map[string]bool{"x": false}, start)
	d.ProcessValues(map[string]bool{"x": false}, start.Add(250*time.Millisecond))
	if !d.IsBaselined() {
		t.Fatal("expected baselined at default debounce")
	}

	on := start.Add(time.Second)
	d.ProcessValues(map[string]bool{"x": true}, on)
	if got := d.ProcessValues(map[string]bool{"x": true}, on.Add(249*time.Millisecond)); len(got) != 0 {
		t.Fatalf("expected no event at 249ms, got %d", len(got))
	}
	if got := d.ProcessValues(map[string]bool{"x": true}, on.Add(250*time.Millisecond)); len(got) != 1 {
		t.Fatalf("expected 1 event at 250ms, got %d", len(got))
	}
}

// --- Heartbeat tests ---

func baselinedForHeartbeat(t *testing.T) *Detector {
	t.Helper()
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, startTime)
	d.ProcessValues(vals(false, false), startTime)
	d.ProcessValues(vals(false, false), startTime.Add(250*time.Millisecond))
	return d
}

func TestCheckHeartbeatDisabledWithZeroInterval(t *testing.T) {
	d := baselinedForHeartbeat(t)
	start := d.StartTime()
	if hb := d.CheckHeartbeat(start.Add(15*time.Minute), 0); hb != nil {
		t.Error("should not return heartbeat when interval is 0 (disabled)")
	}
	if hb := d.CheckHeartbeat(start.Add(15*time.Minute), -1*time.Minute); hb != nil {
		t.Error("should not return heartbeat when interval is negative")
	}
}

func TestCheckHeartbeatBeforeBaseline(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewProfileDetector(twoChannel(), 250*time.Millisecond, startTime)
	d.ProcessValues(vals(false, false), startTime)
	if hb := d.CheckHeartbeat(startTime.Add(15*time.Minute), 15*time.Minute); hb != nil {
		t.Error("should not return heartbeat before baseline")
	}
}

func TestCheckHeartbeatBeforeInterval(t *testing.T) {
	d := baselinedForHeartbeat(t)
	if hb := d.CheckHeartbeat(d.StartTime().Add(14*time.Minute), 15*time.Minute); hb != nil {
		t.Error("should not return heartbeat before interval")
	}
}

func TestCheckHeartbeatAtInterval(t *testing.T) {
	d := baselinedForHeartbeat(t)
	checkTime := d.StartTime().Add(15 * time.Minute)
	hb := d.CheckHeartbeat(checkTime, 15*time.Minute)
	if hb == nil {
		t.Fatal("should return heartbeat at interval")
	}
	if !hb.Timestamp.Equal(checkTime) {
		t.Errorf("expected timestamp %v, got %v", checkTime, hb.Timestamp)
	}
	if hb.Uptime != 15*time.Minute {
		t.Errorf("expected uptime 15m, got %v", hb.Uptime)
	}
}

func TestCheckHeartbeatUpdatesLastTime(t *testing.T) {
	d := baselinedForHeartbeat(t)
	t1 := d.StartTime().Add(15 * time.Minute)
	if hb := d.CheckHeartbeat(t1, 15*time.Minute); hb == nil {
		t.Fatal("should return first heartbeat")
	}
	if hb := d.CheckHeartbeat(t1.Add(time.Second), 15*time.Minute); hb != nil {
		t.Error("should not return heartbeat immediately after previous")
	}
	if hb := d.CheckHeartbeat(t1.Add(15*time.Minute), 15*time.Minute); hb == nil {
		t.Fatal("should return second heartbeat")
	}
}

func TestHeartbeatContainsCounts(t *testing.T) {
	d := baselinedForHeartbeat(t)
	start := d.StartTime()

	t1 := start.Add(500 * time.Millisecond)
	d.ProcessValues(vals(true, false), t1)
	d.ProcessValues(vals(true, false), t1.Add(250*time.Millisecond)) // A_ON

	t2 := t1.Add(500 * time.Millisecond)
	d.ProcessValues(vals(true, true), t2)
	d.ProcessValues(vals(true, true), t2.Add(250*time.Millisecond)) // B_ON

	hb := d.CheckHeartbeat(start.Add(15*time.Minute), 15*time.Minute)
	if hb == nil {
		t.Fatal("should return heartbeat")
	}
	if hb.Counts["A_ON"] != 1 {
		t.Errorf("expected A_ON=1, got %d", hb.Counts["A_ON"])
	}
	if hb.Counts["B_ON"] != 1 {
		t.Errorf("expected B_ON=1, got %d", hb.Counts["B_ON"])
	}
	if hb.Counts["A_OFF"] != 0 {
		t.Errorf("expected A_OFF=0, got %d", hb.Counts["A_OFF"])
	}
}

func TestMultipleHeartbeatsAccumulateCounts(t *testing.T) {
	d := baselinedForHeartbeat(t)
	start := d.StartTime()

	t1 := start.Add(500 * time.Millisecond)
	d.ProcessValues(vals(true, false), t1)
	d.ProcessValues(vals(true, false), t1.Add(250*time.Millisecond)) // A_ON

	hb1 := d.CheckHeartbeat(start.Add(15*time.Minute), 15*time.Minute)
	if hb1.Counts["A_ON"] != 1 {
		t.Errorf("first heartbeat: expected A_ON=1, got %d", hb1.Counts["A_ON"])
	}

	t2 := start.Add(16 * time.Minute)
	d.ProcessValues(vals(false, false), t2)
	d.ProcessValues(vals(false, false), t2.Add(250*time.Millisecond)) // A_OFF

	hb2 := d.CheckHeartbeat(start.Add(30*time.Minute), 15*time.Minute)
	if hb2.Counts["A_ON"] != 1 {
		t.Errorf("second heartbeat: expected A_ON=1, got %d", hb2.Counts["A_ON"])
	}
	if hb2.Counts["A_OFF"] != 1 {
		t.Errorf("second heartbeat: expected A_OFF=1, got %d", hb2.Counts["A_OFF"])
	}
}

// --- Fuzz test ---

// FuzzDetectorProcess feeds random sample sequences into ProcessValues and
// asserts invariants that must hold for any input.
func FuzzDetectorProcess(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0x03, 0x03, 0x03, 0x03})
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x01, 0x01})
	f.Add([]byte{0x01, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x00})

	validNames := map[string]bool{
		"A_ON": true, "A_OFF": true, "B_ON": true, "B_OFF": true,
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}

		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		step := 100 * time.Millisecond
		d := NewProfileDetector(twoChannel(), 250*time.Millisecond, start)

		var (
			lastTimestamp time.Time
			preBaseline   = true
			observed      = map[string]int{}
		)

		for i, b := range data {
			a := b&0x01 != 0
			bb := b&0x02 != 0
			now := start.Add(time.Duration(i+1) * step)

			events := d.ProcessValues(vals(a, bb), now)

			if preBaseline && !d.IsBaselined() {
				if len(events) != 0 {
					t.Fatal("events emitted before baseline established")
				}
				continue
			}
			preBaseline = false

			for _, e := range events {
				if !validNames[e.Name] {
					t.Fatalf("invalid event name: %q", e.Name)
				}
				if e.States["a"] != StateOn && e.States["a"] != StateOff {
					t.Fatalf("invalid a state: %q", e.States["a"])
				}
				if e.States["b"] != StateOn && e.States["b"] != StateOff {
					t.Fatalf("invalid b state: %q", e.States["b"])
				}
				if !lastTimestamp.IsZero() && e.Timestamp.Before(lastTimestamp) {
					t.Fatalf("timestamp went backwards: %v -> %v", lastTimestamp, e.Timestamp)
				}
				lastTimestamp = e.Timestamp
				observed[e.Name]++
			}
		}

		counts := d.GenericCountsSnapshot()
		for name, n := range observed {
			if counts[name] != n {
				t.Errorf("%s count mismatch: detector=%d observed=%d", name, counts[name], n)
			}
		}
	})
}
