package iot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/sweeney/common/iot/gpio"
	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/mqtt"
	"github.com/sweeney/common/iot/profile"
	"github.com/sweeney/common/iot/status"
)

// boilerTestProfile is a 2-channel profile used to exercise the generic runtime
// (the same shape the boiler binary supplies).
func boilerTestProfile() profile.SensorProfile {
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

func noNet() *status.NetworkInfo { return nil }

// fakeClock yields start, start+step, start+2*step, ... on successive calls.
// Only the runtime goroutine calls it, so it needs no synchronization.
func fakeClock(start time.Time, step time.Duration) func() time.Time {
	n := 0
	return func() time.Time {
		t := start.Add(time.Duration(n) * step)
		n++
		return t
	}
}

type chhw struct{ CH, HW bool }

func samplesToBits(samples []chhw) [][]bool {
	out := make([][]bool, len(samples))
	for i, s := range samples {
		out[i] = []bool{s.CH, s.HW}
	}
	return out
}

func repeat(sample chhw, n int) []chhw {
	out := make([]chhw, n)
	for i := range out {
		out[i] = sample
	}
	return out
}

// faultReader returns errors for a fixed range of ReadChannels() calls.
type faultReader struct {
	inner      *gpio.FakeChannelReader
	call       int
	faultStart int
	faultEnd   int
}

func (r *faultReader) ReadChannels() ([]bool, error) {
	i := r.call
	r.call++
	if i >= r.faultStart && i < r.faultEnd {
		return nil, errors.New("gpio fault")
	}
	return r.inner.ReadChannels()
}

func (r *faultReader) Close() error { return r.inner.Close() }

func newReader(samples []chhw) *gpio.FakeChannelReader {
	return gpio.NewFakeChannelReader(samplesToBits(samples))
}

func newPub() *mqtt.FakePublisher {
	return mqtt.NewFakePublisherForProfile(boilerTestProfile())
}

// runScenario drives Run with injected tick/signal channels and a fake clock,
// feeding nTicks then the shutdown signal. NetworkInfo is stubbed to nil.
func runScenario(t *testing.T, reader gpio.ChannelReader, pub *mqtt.FakePublisher, tracker *status.Tracker, debounce, heartbeat time.Duration, clock func() time.Time, nTicks int, sig os.Signal) error {
	t.Helper()
	tickCh := make(chan time.Time)
	sigCh := make(chan os.Signal, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunConfig{
			Profile:     boilerTestProfile(),
			Reader:      reader,
			Publisher:   pub,
			Tracker:     tracker,
			Debounce:    debounce,
			Heartbeat:   heartbeat,
			Now:         clock,
			Tick:        tickCh,
			Signals:     sigCh,
			NetworkInfo: noNet,
		})
	}()
	for i := 0; i < nTicks; i++ {
		tickCh <- time.Time{}
	}
	sigCh <- sig
	return <-errCh
}

func clk() func() time.Time {
	return fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)
}

func countSystem(pub *mqtt.FakePublisher, event string) int {
	n := 0
	for _, se := range pub.SystemEvents {
		if se.Event == event {
			n++
		}
	}
	return n
}

func findSystemEvent(pub *mqtt.FakePublisher, event string) *mqtt.SystemEvent {
	for i := range pub.SystemEvents {
		if pub.SystemEvents[i].Event == event {
			return &pub.SystemEvents[i]
		}
	}
	return nil
}

func TestRunNoEventsAtBaseline(t *testing.T) {
	pub := newPub()
	err := runScenario(t, newReader(repeat(chhw{}, 4)), pub, nil, 250*time.Millisecond, 0, clk(), 4, syscall.SIGTERM)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(pub.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(pub.Events))
	}
	if countSystem(pub, "HEARTBEAT") != 1 {
		t.Error("expected a ready HEARTBEAT")
	}
	if countSystem(pub, "SHUTDOWN") != 1 {
		t.Error("expected a SHUTDOWN")
	}
}

func TestRunSingleTransition(t *testing.T) {
	samples := append(repeat(chhw{}, 4), repeat(chhw{CH: true}, 4)...)
	pub := newPub()
	if err := runScenario(t, newReader(samples), pub, nil, 250*time.Millisecond, 0, clk(), len(samples), syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(pub.Events) != 1 || pub.Events[0].Name != "CH_ON" {
		t.Fatalf("expected one CH_ON event, got %+v", pub.Events)
	}
	if pub.Events[0].States["ch"] != logic.StateOn || pub.Events[0].States["hw"] != logic.StateOff {
		t.Errorf("unexpected states: %+v", pub.Events[0].States)
	}
}

func TestRunMultipleTransitions(t *testing.T) {
	samples := append(repeat(chhw{}, 4),
		append(repeat(chhw{CH: true}, 4),
			append(repeat(chhw{CH: true, HW: true}, 4),
				repeat(chhw{HW: true}, 4)...)...)...)
	pub := newPub()
	if err := runScenario(t, newReader(samples), pub, nil, 250*time.Millisecond, 0, clk(), len(samples), syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	want := []string{"CH_ON", "HW_ON", "CH_OFF"}
	if len(pub.Events) != len(want) {
		t.Fatalf("expected %d events, got %d", len(want), len(pub.Events))
	}
	for i, w := range want {
		if pub.Events[i].Name != w {
			t.Errorf("event %d: got %s, want %s", i, pub.Events[i].Name, w)
		}
	}
}

func TestRunBounceRejection(t *testing.T) {
	samples := append(repeat(chhw{}, 4),
		append([]chhw{{CH: true}}, repeat(chhw{}, 4)...)...)
	pub := newPub()
	if err := runScenario(t, newReader(samples), pub, nil, 250*time.Millisecond, 0, clk(), len(samples), syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(pub.Events) != 0 {
		t.Errorf("expected 0 events (bounce rejected), got %d", len(pub.Events))
	}
}

func TestRunGPIOErrorRecovery(t *testing.T) {
	inner := newReader(append(repeat(chhw{}, 4), repeat(chhw{CH: true}, 4)...))
	reader := &faultReader{inner: inner, faultStart: 4, faultEnd: 7}
	pub := newPub()
	if err := runScenario(t, reader, pub, nil, 250*time.Millisecond, 0, clk(), 11, syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(pub.Events) != 1 || pub.Events[0].Name != "CH_ON" {
		t.Fatalf("expected one CH_ON after recovery, got %+v", pub.Events)
	}
	if countSystem(pub, "SHUTDOWN") != 1 {
		t.Error("expected SHUTDOWN despite GPIO errors")
	}
}

func TestRunPublishErrorContinues(t *testing.T) {
	samples := append(repeat(chhw{}, 4), repeat(chhw{CH: true}, 4)...)
	pub := newPub()
	pub.PublishError = fmt.Errorf("broker unavailable")
	if err := runScenario(t, newReader(samples), pub, nil, 250*time.Millisecond, 0, clk(), len(samples), syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(pub.Events) != 0 {
		t.Errorf("expected 0 recorded events (publish failed), got %d", len(pub.Events))
	}
	if countSystem(pub, "SHUTDOWN") != 1 {
		t.Error("expected SHUTDOWN despite publish errors")
	}
}

func TestRunScheduledHeartbeat(t *testing.T) {
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 5*time.Minute)
	pub := newPub()
	if err := runScenario(t, newReader(repeat(chhw{}, 4)), pub, nil, 10*time.Minute, 15*time.Minute, clock, 4, syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if hb := countSystem(pub, "HEARTBEAT"); hb != 2 {
		t.Errorf("expected 2 HEARTBEAT (ready + scheduled), got %d", hb)
	}
	if countSystem(pub, "SHUTDOWN") != 1 {
		t.Error("expected 1 SHUTDOWN")
	}
}

func TestRunReadyHeartbeatFiresOnce(t *testing.T) {
	pub := newPub()
	if err := runScenario(t, newReader(repeat(chhw{}, 8)), pub, nil, 250*time.Millisecond, 0, clk(), 8, syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if hb := countSystem(pub, "HEARTBEAT"); hb != 1 {
		t.Errorf("expected exactly 1 HEARTBEAT, got %d", hb)
	}
}

func TestRunReadyHeartbeatRetained(t *testing.T) {
	pub := newPub()
	if err := runScenario(t, newReader(repeat(chhw{CH: true}, 4)), pub, nil, 250*time.Millisecond, 0, clk(), 4, syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	hb := findSystemEvent(pub, "HEARTBEAT")
	if hb == nil || !hb.Retained {
		t.Error("ready HEARTBEAT must be present and retained")
	}
}

func TestRunStartupNotRetained(t *testing.T) {
	pub := newPub()
	tr := status.NewTrackerForProfile(boilerTestProfile(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), status.Config{})
	if err := runScenario(t, newReader(repeat(chhw{}, 4)), pub, tr, 250*time.Millisecond, 0, clk(), 4, syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	su := findSystemEvent(pub, "STARTUP")
	if su == nil {
		t.Fatal("expected a STARTUP event")
	}
	if su.Retained {
		t.Error("STARTUP must not be retained (new subscribers would get stale boot state)")
	}
}

func TestRunShutdownSIGINT(t *testing.T) {
	pub := newPub()
	if err := runScenario(t, newReader(repeat(chhw{}, 4)), pub, nil, 250*time.Millisecond, 0, clk(), 4, syscall.SIGINT); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	sd := findSystemEvent(pub, "SHUTDOWN")
	if sd == nil || sd.Reason != "SIGINT" || !sd.Retained {
		t.Errorf("expected retained SHUTDOWN with reason SIGINT, got %+v", sd)
	}
}

func TestRunShutdownSIGTERM(t *testing.T) {
	pub := newPub()
	if err := runScenario(t, newReader(repeat(chhw{}, 4)), pub, nil, 250*time.Millisecond, 0, clk(), 4, syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	sd := findSystemEvent(pub, "SHUTDOWN")
	if sd == nil || sd.Reason != "SIGTERM" || !sd.Retained {
		t.Errorf("expected retained SHUTDOWN with reason SIGTERM, got %+v", sd)
	}
}

func TestRunUpdatesTracker(t *testing.T) {
	samples := append(repeat(chhw{}, 4), repeat(chhw{CH: true}, 4)...)
	pub := newPub()
	pub.Connected = true
	tr := status.NewTrackerForProfile(boilerTestProfile(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), status.Config{})
	if err := runScenario(t, newReader(samples), pub, tr, 250*time.Millisecond, 0, clk(), len(samples), syscall.SIGTERM); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	snap := tr.Snapshot()
	if snap.States["ch"] != logic.StateOn || snap.States["hw"] != logic.StateOff {
		t.Errorf("tracker states: %+v", snap.States)
	}
	if !snap.Baselined {
		t.Error("tracker should be baselined")
	}
	if snap.GenericCounts["CH_ON"] != 1 {
		t.Errorf("tracker CH_ON: got %d, want 1", snap.GenericCounts["CH_ON"])
	}
	if !snap.MQTTConnected {
		t.Error("tracker should show MQTT connected")
	}
}

// statusJSON is a minimal decode struct for asserting heartbeat payload contents.
type statusJSON struct {
	Status struct {
		Event   string `json:"event"`
		Network *struct {
			Type   string `json:"type"`
			IP     string `json:"ip"`
			Status string `json:"status"`
		} `json:"network"`
	} `json:"status"`
}

func TestRunHeartbeatIncludesNetworkInfo(t *testing.T) {
	t.Setenv(envNetworkStatus, "connected")
	t.Setenv(envNetworkType, "wifi")
	t.Setenv(envNetworkIP, "192.168.1.42")
	t.Setenv(envNetworkWifiStatus, "associated")
	t.Setenv(envNetworkWifiSSID, "HomeNet")

	pub := newPub()
	tr := status.NewTrackerForProfile(boilerTestProfile(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), status.Config{})
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 5*time.Minute)

	tickCh := make(chan time.Time)
	sigCh := make(chan os.Signal, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunConfig{
			Profile: boilerTestProfile(), Reader: newReader(repeat(chhw{}, 4)), Publisher: pub, Tracker: tr,
			Debounce: 10 * time.Minute, Heartbeat: 15 * time.Minute,
			Now: clock, Tick: tickCh, Signals: sigCh, // NetworkInfo defaults to ReadNetworkInfoFromEnv
		})
	}()
	for i := 0; i < 4; i++ {
		tickCh <- time.Time{}
	}
	sigCh <- syscall.SIGTERM
	if err := <-errCh; err != nil {
		t.Fatalf("Run error: %v", err)
	}

	var hbPayload []byte
	for i, se := range pub.SystemEvents {
		if se.Event == "HEARTBEAT" {
			hbPayload = pub.SystemPayloads[i]
			break
		}
	}
	if hbPayload == nil {
		t.Fatal("expected a HEARTBEAT system event")
	}
	var parsed statusJSON
	if err := json.Unmarshal(hbPayload, &parsed); err != nil {
		t.Fatalf("invalid HEARTBEAT JSON: %v", err)
	}
	if parsed.Status.Network == nil {
		t.Fatal("HEARTBEAT payload missing network info")
	}
	if parsed.Status.Network.Status != "connected" || parsed.Status.Network.Type != "wifi" || parsed.Status.Network.IP != "192.168.1.42" {
		t.Errorf("unexpected network info: %+v", parsed.Status.Network)
	}
}

func TestReadNetworkInfoFromEnvAllSet(t *testing.T) {
	t.Setenv(envNetworkType, "wifi")
	t.Setenv(envNetworkIP, "192.168.1.100")
	t.Setenv(envNetworkStatus, "connected")
	t.Setenv(envNetworkGateway, "192.168.1.1")
	t.Setenv(envNetworkWifiStatus, "connected")
	t.Setenv(envNetworkWifiSSID, "MyNetwork")

	info := ReadNetworkInfoFromEnv()
	want := &status.NetworkInfo{
		Type: "wifi", IP: "192.168.1.100", Status: "connected",
		Gateway: "192.168.1.1", WifiStatus: "connected", SSID: "MyNetwork",
	}
	if info == nil || *info != *want {
		t.Errorf("ReadNetworkInfoFromEnv: got %+v, want %+v", info, want)
	}
}

func TestReadNetworkInfoFromEnvNoneSet(t *testing.T) {
	for _, k := range []string{envNetworkType, envNetworkIP, envNetworkStatus, envNetworkGateway, envNetworkWifiStatus, envNetworkWifiSSID} {
		t.Setenv(k, "")
	}
	if info := ReadNetworkInfoFromEnv(); info != nil {
		t.Errorf("expected nil when NETWORK_STATUS unset, got %+v", info)
	}
}

func TestEnvVarNames(t *testing.T) {
	want := map[string]string{
		"NETWORK_TYPE":        envNetworkType,
		"NETWORK_IP":          envNetworkIP,
		"NETWORK_STATUS":      envNetworkStatus,
		"NETWORK_GATEWAY":     envNetworkGateway,
		"NETWORK_WIFI_STATUS": envNetworkWifiStatus,
		"NETWORK_WIFI_SSID":   envNetworkWifiSSID,
	}
	for canonical, got := range want {
		if got != canonical {
			t.Errorf("env var constant: got %q, want %q", got, canonical)
		}
	}
}
