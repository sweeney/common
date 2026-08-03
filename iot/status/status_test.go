package status

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/profile"
)

func twoChannel() profile.SensorProfile {
	return profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "a", Label: "Alpha", OnEvent: "A_ON", OffEvent: "A_OFF"},
			{Key: "b", Label: "Bravo", OnEvent: "B_ON", OffEvent: "B_OFF"},
		},
	}
}

func newTracker(start time.Time, cfg Config) *Tracker {
	return NewTrackerForProfile(twoChannel(), start, cfg)
}

func TestNewTracker(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := Config{PollMs: 100, DebounceMs: 250, Broker: "tcp://localhost:1883", HTTPPort: ":80"}
	tr := newTracker(start, cfg)

	snap := tr.Snapshot()
	if !snap.StartTime.Equal(start) {
		t.Errorf("StartTime: got %v, want %v", snap.StartTime, start)
	}
	if snap.Config.PollMs != 100 {
		t.Errorf("Config.PollMs: got %d, want 100", snap.Config.PollMs)
	}
	if snap.Baselined {
		t.Error("expected Baselined=false initially")
	}
	if snap.MQTTConnected {
		t.Error("expected MQTTConnected=false initially")
	}
}

func TestUpdateAndSnapshot(t *testing.T) {
	tr := newTracker(time.Now(), Config{})
	tr.UpdateChannels(
		map[string]logic.State{"a": logic.StateOn, "b": logic.StateOff},
		true,
		map[string]int{"A_ON": 3, "B_OFF": 1},
	)

	snap := tr.Snapshot()
	if snap.States["a"] != logic.StateOn {
		t.Errorf("a: got %q, want ON", snap.States["a"])
	}
	if snap.States["b"] != logic.StateOff {
		t.Errorf("b: got %q, want OFF", snap.States["b"])
	}
	if !snap.Baselined {
		t.Error("expected Baselined=true")
	}
	if snap.GenericCounts["A_ON"] != 3 {
		t.Errorf("A_ON: got %d, want 3", snap.GenericCounts["A_ON"])
	}
}

func TestSetMQTTConnected(t *testing.T) {
	tr := newTracker(time.Now(), Config{})
	tr.SetMQTTConnected(true)
	if !tr.Snapshot().MQTTConnected {
		t.Error("expected MQTTConnected=true")
	}
	tr.SetMQTTConnected(false)
	if tr.Snapshot().MQTTConnected {
		t.Error("expected MQTTConnected=false")
	}
}

func TestSetNetwork(t *testing.T) {
	tr := newTracker(time.Now(), Config{})
	if tr.Snapshot().Network != nil {
		t.Error("expected nil Network initially")
	}
	tr.SetNetwork(&NetworkInfo{Type: "wifi", IP: "192.168.1.42", Status: "connected"})
	snap := tr.Snapshot()
	if snap.Network == nil {
		t.Fatal("expected non-nil Network")
	}
	if snap.Network.IP != "192.168.1.42" {
		t.Errorf("Network.IP: got %q", snap.Network.IP)
	}
}

func TestSnapshotUptime(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{StartTime: start, Now: start.Add(15 * time.Minute)}
	if snap.Uptime() != 15*time.Minute {
		t.Errorf("Uptime: got %v, want 15m", snap.Uptime())
	}
}

func TestSnapshotNowIsSet(t *testing.T) {
	tr := newTracker(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Config{})
	before := time.Now()
	snap := tr.Snapshot()
	after := time.Now()
	if snap.Now.Before(before) || snap.Now.After(after) {
		t.Errorf("Now (%v) not between %v and %v", snap.Now, before, after)
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	tr := newTracker(time.Now(), Config{})
	tr.UpdateChannels(map[string]logic.State{"a": logic.StateOn}, true, map[string]int{"A_ON": 1})
	snap1 := tr.Snapshot()
	tr.UpdateChannels(map[string]logic.State{"a": logic.StateOff}, true, map[string]int{"A_ON": 1, "A_OFF": 1})
	if snap1.States["a"] != logic.StateOn {
		t.Error("snapshot should be a copy; a was modified")
	}
}

// statusFields decodes the inner "status" object as a generic map.
func statusFields(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	status, ok := raw["status"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing status object in %s", data)
	}
	return status
}

func baselineSnap() Snapshot {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return Snapshot{
		Profile:       twoChannel(),
		States:        map[string]logic.State{"a": logic.StateOn, "b": logic.StateOff},
		GenericCounts: map[string]int{"A_ON": 5, "A_OFF": 2, "B_ON": 1, "B_OFF": 0},
		Baselined:     true,
		StartTime:     start,
		Now:           start.Add(15 * time.Minute),
		MQTTConnected: true,
		Config:        Config{PollMs: 100, DebounceMs: 250, HeartbeatMs: 900000, Broker: "tcp://localhost:1883", HTTPPort: ":80"},
	}
}

func TestFormatJSON(t *testing.T) {
	status := statusFields(t, FormatJSON(baselineSnap()))

	if status["a"] != "ON" {
		t.Errorf("a: got %v, want ON", status["a"])
	}
	if status["b"] != "OFF" {
		t.Errorf("b: got %v, want OFF", status["b"])
	}
	if status["ready"] != true {
		t.Error("expected ready=true")
	}
	if status["uptime_seconds"].(float64) != 900 {
		t.Errorf("uptime_seconds: got %v, want 900", status["uptime_seconds"])
	}
	mqtt := status["mqtt"].(map[string]interface{})
	if mqtt["connected"] != true {
		t.Error("expected mqtt.connected=true")
	}
	counts := status["event_counts"].(map[string]interface{})
	if counts["a_on"].(float64) != 5 {
		t.Errorf("a_on: got %v, want 5", counts["a_on"])
	}
	if _, exists := status["event"]; exists {
		t.Error("expected no event field for web format")
	}
}

func TestFormatJSONUnknownState(t *testing.T) {
	snap := Snapshot{
		Profile:   twoChannel(),
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Now:       time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
	}
	status := statusFields(t, FormatJSON(snap))
	if status["a"] != "UNKNOWN" {
		t.Errorf("a: got %v, want UNKNOWN", status["a"])
	}
	if status["b"] != "UNKNOWN" {
		t.Errorf("b: got %v, want UNKNOWN", status["b"])
	}
}

func TestFormatStatusEvent(t *testing.T) {
	status := statusFields(t, FormatStatusEvent(baselineSnap(), "HEARTBEAT", ""))
	if status["event"] != "HEARTBEAT" {
		t.Errorf("event: got %v, want HEARTBEAT", status["event"])
	}
	if _, exists := status["reason"]; exists {
		t.Error("reason should be omitted when empty")
	}
	if status["a"] != "ON" {
		t.Errorf("a: got %v, want ON", status["a"])
	}
}

func TestFormatStatusEventShutdown(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{
		Profile:   twoChannel(),
		States:    map[string]logic.State{"a": logic.StateOff, "b": logic.StateOff},
		Baselined: true,
		StartTime: start,
		Now:       start.Add(30 * time.Minute),
		Config:    Config{Broker: "tcp://localhost:1883"},
	}
	status := statusFields(t, FormatStatusEvent(snap, "SHUTDOWN", "SIGTERM"))
	if status["event"] != "SHUTDOWN" {
		t.Errorf("event: got %v, want SHUTDOWN", status["event"])
	}
	if status["reason"] != "SIGTERM" {
		t.Errorf("reason: got %v, want SIGTERM", status["reason"])
	}
}

func TestFormatJSONWithNetwork(t *testing.T) {
	snap := Snapshot{
		Profile:   twoChannel(),
		States:    map[string]logic.State{"a": logic.StateOn, "b": logic.StateOff},
		Baselined: true,
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Now:       time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
		Network:   &NetworkInfo{Type: "wifi", IP: "192.168.1.42", Status: "connected", SSID: "MyNet"},
		Config:    Config{Broker: "tcp://localhost:1883"},
	}
	status := statusFields(t, FormatJSON(snap))
	net, ok := status["network"].(map[string]interface{})
	if !ok {
		t.Fatal("expected network in JSON")
	}
	if net["ip"] != "192.168.1.42" {
		t.Errorf("network.ip: got %v", net["ip"])
	}
	if net["ssid"] != "MyNet" {
		t.Errorf("network.ssid: got %v", net["ssid"])
	}
}

func TestConcurrentAccess(t *testing.T) {
	tr := newTracker(time.Now(), Config{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			tr.UpdateChannels(map[string]logic.State{"a": logic.StateOn}, true, map[string]int{"A_ON": i})
			tr.SetMQTTConnected(i%2 == 0)
			tr.SetNetwork(&NetworkInfo{IP: "1.2.3.4"})
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			snap := tr.Snapshot()
			_ = snap.Uptime()
		}
	}()
	wg.Wait()
}
