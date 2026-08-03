// Package status provides a thread-safe status tracker for sensor daemons.
// It is designed to be read by HTTP handlers and (future) LED drivers.
//
// The tracker is sensor-agnostic: it holds per-channel states and per-event
// counts keyed by the profile's channel/event vocabulary. Serialization writes
// channels and counts in profile order via an ordered encoder (see json.go).
package status

import (
	"sync"
	"time"

	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/profile"
)

// NetworkInfo contains network state. This is a local copy to avoid
// importing the mqtt package from status.
type NetworkInfo struct {
	Type       string
	IP         string
	Status     string
	Gateway    string
	WifiStatus string
	SSID       string
}

// Config contains daemon configuration for display.
type Config struct {
	PollMs      int64
	DebounceMs  int64
	HeartbeatMs int64
	Broker      string
	HTTPPort    string
	WSBroker    string // Websocket broker URL for browser MQTT (empty = disabled)
}

// Snapshot is a point-in-time view of daemon state.
// It is a value type — safe to use after the lock is released.
type Snapshot struct {
	// Profile drives serialization: channel order, keys, and event-count keys.
	Profile profile.SensorProfile
	// States holds the current logical state of every channel, keyed by key.
	States map[string]logic.State
	// GenericCounts holds emitted-event counts keyed by event name.
	GenericCounts map[string]int

	Baselined     bool
	StartTime     time.Time
	Now           time.Time
	MQTTConnected bool
	Network       *NetworkInfo
	Config        Config
}

// Uptime returns the duration since the daemon started.
func (s Snapshot) Uptime() time.Duration {
	return s.Now.Sub(s.StartTime)
}

// Tracker holds mutable daemon state behind an RWMutex.
type Tracker struct {
	mu   sync.RWMutex
	snap Snapshot
}

// NewTrackerForProfile creates a Tracker for a sensor profile.
func NewTrackerForProfile(prof profile.SensorProfile, startTime time.Time, cfg Config) *Tracker {
	return &Tracker{
		snap: Snapshot{
			Profile:       prof,
			States:        make(map[string]logic.State),
			GenericCounts: make(map[string]int),
			StartTime:     startTime,
			Config:        cfg,
		},
	}
}

// UpdateChannels sets per-channel states, baseline status, and generic event
// counts (keyed by event name).
func (t *Tracker) UpdateChannels(states map[string]logic.State, baselined bool, counts map[string]int) {
	t.mu.Lock()
	t.snap.States = states
	t.snap.GenericCounts = counts
	t.snap.Baselined = baselined
	t.mu.Unlock()
}

// SetMQTTConnected sets the MQTT connection status.
func (t *Tracker) SetMQTTConnected(connected bool) {
	t.mu.Lock()
	t.snap.MQTTConnected = connected
	t.mu.Unlock()
}

// SetNetwork sets the network info.
func (t *Tracker) SetNetwork(info *NetworkInfo) {
	t.mu.Lock()
	t.snap.Network = info
	t.mu.Unlock()
}

// Snapshot returns a point-in-time copy of the daemon state.
// The Now field is set to the current time at the moment of the call.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	s := t.snap
	t.mu.RUnlock()
	s.Now = time.Now()
	return s
}
