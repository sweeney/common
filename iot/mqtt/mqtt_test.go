package mqtt

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/profile"
)

// twoChannelProfile is a generic 2-channel profile for serialization tests.
func twoChannelProfile() profile.SensorProfile {
	return profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "a", OnEvent: "A_ON", OffEvent: "A_OFF"},
			{Key: "b", OnEvent: "B_ON", OffEvent: "B_OFF"},
		},
		EventTopic:  "sensor/events",
		SystemTopic: "sensor/system",
		PayloadRoot: "sensor",
	}
}

func TestFormatEventPayloadOrderedShape(t *testing.T) {
	p := twoChannelProfile()
	ts := time.Date(2026, 2, 2, 22, 18, 12, 0, time.UTC)
	states := map[string]logic.State{"a": logic.StateOn, "b": logic.StateOff}

	got := string(FormatEventPayload(p, ts, "A_ON", states))
	want := `{"sensor":{"timestamp":"2026-02-02T22:18:12Z","event":"A_ON","a":{"state":"ON"},"b":{"state":"OFF"}}}`
	if got != want {
		t.Errorf("payload:\n got:  %s\n want: %s", got, want)
	}
}

func TestFormatEventPayloadTimezoneConversion(t *testing.T) {
	p := twoChannelProfile()
	loc, _ := time.LoadLocation("America/New_York")
	localTime := time.Date(2026, 2, 3, 10, 30, 0, 0, loc) // 10:30 EST = 15:30 UTC
	states := map[string]logic.State{"a": logic.StateOn, "b": logic.StateOff}

	got := string(FormatEventPayload(p, localTime, "A_ON", states))
	want := `{"sensor":{"timestamp":"2026-02-03T15:30:00Z","event":"A_ON","a":{"state":"ON"},"b":{"state":"OFF"}}}`
	if got != want {
		t.Errorf("payload:\n got:  %s\n want: %s", got, want)
	}
}

func TestFormatEventPayloadSingleChannel(t *testing.T) {
	p := profile.SensorProfile{
		Channels:    []profile.Channel{{Key: "button", OnEvent: "PRESS", OffEvent: "RELEASE"}},
		PayloadRoot: "doorbell",
	}
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := string(FormatEventPayload(p, ts, "PRESS", map[string]logic.State{"button": logic.StateOn}))
	want := `{"doorbell":{"timestamp":"2026-01-01T00:00:00Z","event":"PRESS","button":{"state":"ON"}}}`
	if got != want {
		t.Errorf("payload:\n got:  %s\n want: %s", got, want)
	}
}

func TestFakePublisherPublishEvent(t *testing.T) {
	f := NewFakePublisherForProfile(twoChannelProfile())
	event := logic.GenericEvent{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Key:       "a",
		Name:      "A_ON",
		States:    map[string]logic.State{"a": logic.StateOn, "b": logic.StateOff},
	}

	if err := f.PublishEvent(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(f.Events))
	}
	if len(f.Payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(f.Payloads))
	}
	want := `{"sensor":{"timestamp":"2026-01-01T00:00:00Z","event":"A_ON","a":{"state":"ON"},"b":{"state":"OFF"}}}`
	if string(f.Payloads[0]) != want {
		t.Errorf("payload:\n got:  %s\n want: %s", f.Payloads[0], want)
	}
}

func TestFakePublisherPublishEventError(t *testing.T) {
	f := NewFakePublisherForProfile(twoChannelProfile())
	f.PublishError = errors.New("simulated error")

	if err := f.PublishEvent(logic.GenericEvent{Name: "A_ON"}); err == nil {
		t.Error("expected error")
	}
	if len(f.Events) != 0 {
		t.Errorf("expected no events recorded on error, got %d", len(f.Events))
	}
}

func TestFakePublisherClose(t *testing.T) {
	f := NewFakePublisher()
	if f.Closed {
		t.Error("should not be closed initially")
	}
	if err := f.Close(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !f.Closed {
		t.Error("should be closed after Close()")
	}
}

func TestFakePublisherReset(t *testing.T) {
	f := NewFakePublisherForProfile(twoChannelProfile())
	f.PublishEvent(logic.GenericEvent{Name: "A_ON", States: map[string]logic.State{}})
	f.PublishSystem(SystemEvent{Event: "SHUTDOWN", Reason: "SIGTERM"})
	f.Close()
	f.PublishError = errors.New("error")
	f.PublishSystemError = errors.New("error")

	f.Reset()

	if len(f.Events) != 0 || len(f.Payloads) != 0 {
		t.Error("events/payloads should be cleared")
	}
	if len(f.SystemEvents) != 0 || len(f.SystemPayloads) != 0 {
		t.Error("system events/payloads should be cleared")
	}
	if f.Closed {
		t.Error("closed should be reset")
	}
	if f.PublishError != nil || f.PublishSystemError != nil {
		t.Error("errors should be cleared")
	}
}

func TestFakePublisherIsConnected(t *testing.T) {
	f := NewFakePublisher()
	if f.IsConnected() {
		t.Error("expected not connected initially")
	}
	f.Connected = true
	if !f.IsConnected() {
		t.Error("expected connected")
	}
}

func TestClientOptionsMaxReconnectInterval(t *testing.T) {
	p := &RealPublisher{topic: "t", systemTopic: "s", buf: newRingBuffer(100)}
	opts := buildClientOptions("tcp://localhost:1883", p, nil)
	if opts.MaxReconnectInterval != 30*time.Second {
		t.Errorf("MaxReconnectInterval: got %v, want 30s", opts.MaxReconnectInterval)
	}
}

func TestClientOptionsConnectRetryInterval(t *testing.T) {
	p := &RealPublisher{topic: "t", systemTopic: "s", buf: newRingBuffer(100)}
	opts := buildClientOptions("tcp://localhost:1883", p, nil)
	if opts.ConnectRetryInterval != 5*time.Second {
		t.Errorf("ConnectRetryInterval: got %v, want 5s", opts.ConnectRetryInterval)
	}
}

func TestClientOptionsDefaultClientID(t *testing.T) {
	p := &RealPublisher{topic: "t", systemTopic: "s", buf: newRingBuffer(100)}
	opts := buildClientOptions("tcp://localhost:1883", p, nil)
	if opts.ClientID != "iot-sensor" {
		t.Errorf("ClientID: got %q, want iot-sensor", opts.ClientID)
	}
}

func TestClientOptionsWillSetWhenPayloadProvided(t *testing.T) {
	p := &RealPublisher{topic: "energy/x/events", systemTopic: "energy/x/system", buf: newRingBuffer(100)}
	will := []byte(`{"system":{"event":"SHUTDOWN"}}`)
	opts := buildClientOptions("tcp://localhost:1883", p, will)
	if !opts.WillEnabled {
		t.Error("expected will to be enabled when payload provided")
	}
	if opts.WillTopic != "energy/x/system" {
		t.Errorf("will topic: got %s, want energy/x/system", opts.WillTopic)
	}
}

func TestClientOptionsNoWillWhenPayloadNil(t *testing.T) {
	p := &RealPublisher{topic: "t", systemTopic: "s", buf: newRingBuffer(100)}
	opts := buildClientOptions("tcp://localhost:1883", p, nil)
	if opts.WillEnabled {
		t.Error("expected will to be disabled when payload is nil")
	}
}

func TestFormatSystemPayload(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 10, 30, 45, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}
	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{"system":{"timestamp":"2026-02-03T10:30:45Z","event":"SHUTDOWN","reason":"SIGTERM"}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadTimezoneConversion(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/London")
	localTime := time.Date(2026, 7, 15, 14, 0, 0, 0, loc) // 14:00 BST = 13:00 UTC
	event := SystemEvent{Timestamp: localTime, Event: "SHUTDOWN", Reason: "SIGTERM"}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.System.Timestamp != "2026-07-15T13:00:00Z" {
		t.Errorf("expected UTC timestamp 2026-07-15T13:00:00Z, got %s", parsed.System.Timestamp)
	}
}

func TestFormatSystemPayloadRawPayloadBypass(t *testing.T) {
	raw := []byte(`{"status":{"event":"STARTUP"}}`)
	event := SystemEvent{
		Timestamp:  time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:      "STARTUP",
		RawPayload: raw,
	}
	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(payload) != string(raw) {
		t.Errorf("expected RawPayload returned directly:\ngot:  %s\nwant: %s", string(payload), string(raw))
	}
}

func TestFormatSystemPayloadOmitsReasonWhenEmpty(t *testing.T) {
	event := SystemEvent{Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC), Event: "STARTUP"}
	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	system := parsed["system"].(map[string]interface{})
	if _, exists := system["reason"]; exists {
		t.Error("reason field should be omitted for startup events")
	}
}

func TestFormatSystemPayloadOmitsSourceWhenEmpty(t *testing.T) {
	event := SystemEvent{Timestamp: time.Date(2026, 2, 10, 8, 30, 0, 0, time.UTC), Event: "SHUTDOWN", Reason: "SIGTERM"}
	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	system := parsed["system"].(map[string]interface{})
	if _, exists := system["source"]; exists {
		t.Error("source field should be omitted for application-published events")
	}
}

func TestWillPayloadFormat(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 10, 8, 30, 0, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "MQTT_DISCONNECT",
		Source:    "last_will",
	}
	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{"system":{"timestamp":"2026-02-10T08:30:00Z","event":"SHUTDOWN","reason":"MQTT_DISCONNECT","source":"last_will"}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadReconnected(t *testing.T) {
	event := SystemEvent{Timestamp: time.Date(2026, 2, 10, 14, 30, 0, 0, time.UTC), Event: "RECONNECTED"}
	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{"system":{"timestamp":"2026-02-10T14:30:00Z","event":"RECONNECTED"}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFakePublisherPublishSystem(t *testing.T) {
	f := NewFakePublisher()
	if err := f.PublishSystem(SystemEvent{Event: "SHUTDOWN", Reason: "SIGTERM"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(f.SystemEvents))
	}
	if len(f.SystemPayloads) != 1 {
		t.Fatalf("expected 1 system payload, got %d", len(f.SystemPayloads))
	}
}

func TestFakePublisherPublishSystemError(t *testing.T) {
	f := NewFakePublisher()
	f.PublishSystemError = errors.New("simulated error")
	if err := f.PublishSystem(SystemEvent{Event: "SHUTDOWN"}); err == nil {
		t.Error("expected error")
	}
	if len(f.SystemEvents) != 0 {
		t.Errorf("expected no system events recorded on error, got %d", len(f.SystemEvents))
	}
}

func TestFakePublisherRecordsRetainedFlag(t *testing.T) {
	f := NewFakePublisher()
	f.PublishSystem(SystemEvent{Event: "STARTUP", Retained: true})
	f.PublishSystem(SystemEvent{Event: "HEARTBEAT", Retained: false})

	if len(f.SystemEvents) != 2 {
		t.Fatalf("expected 2 system events, got %d", len(f.SystemEvents))
	}
	if !f.SystemEvents[0].Retained {
		t.Error("first event should have Retained=true")
	}
	if f.SystemEvents[1].Retained {
		t.Error("second event should have Retained=false")
	}
}

func TestFakePublisherRawPayloadPassthrough(t *testing.T) {
	f := NewFakePublisher()
	raw := []byte(`{"status":{"event":"HEARTBEAT"}}`)
	if err := f.PublishSystem(SystemEvent{Event: "HEARTBEAT", RawPayload: raw}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.SystemPayloads) != 1 || string(f.SystemPayloads[0]) != string(raw) {
		t.Errorf("expected RawPayload in system payloads, got %v", f.SystemPayloads)
	}
}

// Interface compliance at compile time.
var (
	_ EventPublisher   = (*FakePublisher)(nil)
	_ ConnectionStatus = (*FakePublisher)(nil)
	_ EventPublisher   = (*RealPublisher)(nil)
	_ ConnectionStatus = (*RealPublisher)(nil)
)
