// Package iot_test holds the wire-contract self-test for the generic engine.
//
// It proves two things using only the generic, sensor-agnostic API:
//
//  1. A 2-channel boiler-shaped profile (keys ch/hw, root "boiler", events
//     CH_ON/CH_OFF + HW_ON/HW_OFF) reproduces the EXACT legacy boiler bytes for
//     the canonical scenario (both off -> CH on -> HW on -> CH off) plus a
//     heartbeat. The expected string literals are copied verbatim from the
//     boiler-sensor oracle (internal/wire_contract_test.go) and must not change.
//
//  2. A 1-channel doorbell-shaped profile (key button, root "doorbell",
//     PRESS/RELEASE) serializes to the expected single-channel shape.
package iot_test

import (
	"testing"
	"time"

	"github.com/sweeney/common/iot/gpio"
	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/mqtt"
	"github.com/sweeney/common/iot/profile"
	"github.com/sweeney/common/iot/status"
)

// boilerProfile is the 2-channel boiler-shaped profile under test. Everything
// boiler-specific lives here, not in the engine packages.
func boilerProfile() profile.SensorProfile {
	return profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "ch", Label: "Central Heating", Pin: 26, ActiveLow: true, OnEvent: "CH_ON", OffEvent: "CH_OFF"},
			{Key: "hw", Label: "Hot Water", Pin: 16, ActiveLow: true, OnEvent: "HW_ON", OffEvent: "HW_OFF"},
		},
		EventTopic:  "energy/boiler/sensor/events",
		SystemTopic: "energy/boiler/sensor/system",
		PayloadRoot: "boiler",
		WebTitle:    "Boiler Sensor",
	}
}

func TestWireContractBoilerCanonicalSequence(t *testing.T) {
	prof := boilerProfile()

	// Logical (post-inversion) samples, [ch, hw], one per poll tick.
	samples := [][]bool{
		{false, false}, // t=0
		{false, false}, // t=100ms
		{false, false}, // t=200ms
		{false, false}, // t=300ms (baseline established)
		{true, false},  // t=400ms - CH transition starts
		{true, false},  // t=500ms
		{true, false},  // t=600ms
		{true, false},  // t=700ms - CH_ON confirmed
		{true, true},   // t=800ms - HW transition starts
		{true, true},   // t=900ms
		{true, true},   // t=1000ms
		{true, true},   // t=1100ms - HW_ON confirmed
		{false, true},  // t=1200ms - CH off transition starts
		{false, true},  // t=1300ms
		{false, true},  // t=1400ms
		{false, true},  // t=1500ms - CH_OFF confirmed
	}

	gpioReader := gpio.NewFakeChannelReader(samples)
	publisher := mqtt.NewFakePublisherForProfile(prof)
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewProfileDetector(prof, 250*time.Millisecond, startTime)
	keys := prof.Keys()
	pollInterval := 100 * time.Millisecond

	for i := range samples {
		vals, err := gpioReader.ReadChannels()
		if err != nil {
			t.Fatalf("sample %d: gpio read error: %v", i, err)
		}
		values := make(map[string]bool, len(keys))
		for j, k := range keys {
			values[k] = vals[j]
		}
		now := startTime.Add(time.Duration(i) * pollInterval)
		for _, event := range detector.ProcessValues(values, now) {
			if err := publisher.PublishEvent(event); err != nil {
				t.Fatalf("sample %d: publish error: %v", i, err)
			}
		}
	}

	// Event payloads (topic energy/boiler/sensor/events), in publish order.
	// These literals are copied verbatim from the boiler-sensor oracle.
	wantEvents := []string{
		`{"boiler":{"timestamp":"2026-01-01T12:00:00Z","event":"CH_ON","ch":{"state":"ON"},"hw":{"state":"OFF"}}}`,
		`{"boiler":{"timestamp":"2026-01-01T12:00:01Z","event":"HW_ON","ch":{"state":"ON"},"hw":{"state":"ON"}}}`,
		`{"boiler":{"timestamp":"2026-01-01T12:00:01Z","event":"CH_OFF","ch":{"state":"OFF"},"hw":{"state":"ON"}}}`,
	}
	if len(publisher.Payloads) != len(wantEvents) {
		t.Fatalf("expected %d event payloads, got %d", len(wantEvents), len(publisher.Payloads))
	}
	for i, want := range wantEvents {
		if got := string(publisher.Payloads[i]); got != want {
			t.Errorf("event payload %d:\n got:  %s\n want: %s", i, got, want)
		}
	}

	// Heartbeat payload (topic energy/boiler/sensor/system), built from the
	// status snapshot the way the daemon does.
	tracker := status.NewTrackerForProfile(prof, startTime, status.Config{
		PollMs:     100,
		DebounceMs: 250,
		Broker:     "tcp://192.168.1.200:1883",
	})
	tracker.UpdateChannels(detector.CurrentStates(), detector.IsBaselined(), detector.GenericCountsSnapshot())
	snap := tracker.Snapshot()
	snap.Now = startTime.Add(15 * time.Minute) // deterministic heartbeat timestamp
	raw := status.FormatStatusEvent(snap, "HEARTBEAT", "")

	// Copied verbatim from the boiler-sensor oracle.
	wantHeartbeat := `{"status":{"event":"HEARTBEAT","ch":"OFF","hw":"ON","ready":true,"uptime_seconds":900,"start_time":"2026-01-01T12:00:00Z","timestamp":"2026-01-01T12:15:00Z","mqtt":{"connected":false,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":1,"ch_off":1,"hw_on":1,"hw_off":0},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":0,"broker":"tcp://192.168.1.200:1883","http_port":""}}}`
	if got := string(raw); got != wantHeartbeat {
		t.Errorf("heartbeat payload:\n got:  %s\n want: %s", got, wantHeartbeat)
	}
}

// TestWireContractDoorbellShape proves the 1-channel doorbell-shaped profile
// (key button, root "doorbell", PRESS/RELEASE) serializes to the expected
// single-channel shape — both the event payload and the status JSON keys
// (button, button_on/button_off).
func TestWireContractDoorbellShape(t *testing.T) {
	prof := profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "button", Label: "Doorbell", Pin: 17, ActiveLow: true, OnEvent: "PRESS", OffEvent: "RELEASE", Debounce: 50 * time.Millisecond},
		},
		EventTopic:  "home/doorbell/sensor/events",
		SystemTopic: "home/doorbell/sensor/system",
		PayloadRoot: "doorbell",
		WebTitle:    "Doorbell Sensor",
	}

	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewProfileDetector(prof, 250*time.Millisecond, startTime)
	publisher := mqtt.NewFakePublisherForProfile(prof)

	// Baseline (released) using the channel's 50ms debounce.
	detector.ProcessValues(map[string]bool{"button": false}, startTime)
	detector.ProcessValues(map[string]bool{"button": false}, startTime.Add(50*time.Millisecond))

	// Press, held past debounce.
	press := startTime.Add(200 * time.Millisecond)
	detector.ProcessValues(map[string]bool{"button": true}, press)
	for _, e := range detector.ProcessValues(map[string]bool{"button": true}, press.Add(50*time.Millisecond)) {
		if err := publisher.PublishEvent(e); err != nil {
			t.Fatalf("publish error: %v", err)
		}
	}

	if len(publisher.Payloads) != 1 {
		t.Fatalf("expected 1 PRESS payload, got %d", len(publisher.Payloads))
	}
	wantEvent := `{"doorbell":{"timestamp":"2026-01-01T12:00:00Z","event":"PRESS","button":{"state":"ON"}}}`
	if got := string(publisher.Payloads[0]); got != wantEvent {
		t.Errorf("press payload:\n got:  %s\n want: %s", got, wantEvent)
	}

	// Status JSON uses the single channel's keys (button, button_on/button_off).
	tracker := status.NewTrackerForProfile(prof, startTime, status.Config{PollMs: 20, DebounceMs: 50, Broker: "tcp://192.168.1.200:1883"})
	tracker.UpdateChannels(detector.CurrentStates(), detector.IsBaselined(), detector.GenericCountsSnapshot())
	snap := tracker.Snapshot()
	snap.Now = startTime.Add(15 * time.Minute)
	raw := status.FormatStatusEvent(snap, "HEARTBEAT", "")

	wantStatus := `{"status":{"event":"HEARTBEAT","button":"ON","ready":true,"uptime_seconds":900,"start_time":"2026-01-01T12:00:00Z","timestamp":"2026-01-01T12:15:00Z","mqtt":{"connected":false,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"button_on":1,"button_off":0},"config":{"poll_ms":20,"debounce_ms":50,"heartbeat_ms":0,"broker":"tcp://192.168.1.200:1883","http_port":""}}}`
	if got := string(raw); got != wantStatus {
		t.Errorf("doorbell status payload:\n got:  %s\n want: %s", got, wantStatus)
	}
}
