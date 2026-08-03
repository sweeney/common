package status

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/sweeney/common/iot/logic"
)

// MQTTStatus reports MQTT connection state.
type MQTTStatus struct {
	Connected bool   `json:"connected"`
	Broker    string `json:"broker"`
}

// NetworkJSON is the JSON representation of network info.
type NetworkJSON struct {
	Type       string `json:"type"`
	IP         string `json:"ip"`
	Status     string `json:"status"`
	Gateway    string `json:"gateway"`
	WifiStatus string `json:"wifi_status"`
	SSID       string `json:"ssid"`
}

// ConfigJSON is the JSON representation of daemon config.
type ConfigJSON struct {
	PollMs      int64  `json:"poll_ms"`
	DebounceMs  int64  `json:"debounce_ms"`
	HeartbeatMs int64  `json:"heartbeat_ms"`
	Broker      string `json:"broker"`
	HTTPPort    string `json:"http_port"`
	WSBroker    string `json:"ws_broker,omitempty"`
}

// orderedObject is a JSON object that marshals its members in insertion order
// (unlike a Go map, which json sorts alphabetically). Nested values are stored
// as already-marshalled bytes (json.RawMessage), so the whole tree serializes
// deterministically and is safe to re-indent with json.Indent.
type orderedObject struct {
	keys   []string
	values []json.RawMessage
}

func (o *orderedObject) set(key string, raw json.RawMessage) {
	o.keys = append(o.keys, key)
	o.values = append(o.values, raw)
}

// setVal marshals v (a plain struct/scalar) and stores it.
func (o *orderedObject) setVal(key string, v interface{}) {
	raw, _ := json.Marshal(v)
	o.set(key, raw)
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.values[i])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// stateOrUnknown maps an empty/unknown channel state to "UNKNOWN".
func stateOrUnknown(s logic.State) string {
	if s == "" {
		return "UNKNOWN"
	}
	return string(s)
}

// buildStatusObject constructs the ordered "status" inner object for a snapshot.
//
// Field order is fixed (it pins the wire contract); only the per-channel state
// keys and per-channel event-count keys are profile-driven. Channels are
// emitted in profile order, and counts as "<key>_on","<key>_off" per channel.
func buildStatusObject(snap Snapshot, event, reason string) orderedObject {
	var inner orderedObject

	if event != "" {
		inner.setVal("event", event)
	}
	if reason != "" {
		inner.setVal("reason", reason)
	}

	// Per-channel states, in profile order.
	for _, c := range snap.Profile.Channels {
		inner.setVal(c.Key, stateOrUnknown(snap.States[c.Key]))
	}

	inner.setVal("ready", snap.Baselined)
	inner.setVal("uptime_seconds", int64(snap.Uptime().Truncate(time.Second).Seconds()))
	inner.setVal("start_time", snap.StartTime.UTC().Format(time.RFC3339))
	inner.setVal("timestamp", snap.Now.UTC().Format(time.RFC3339))
	inner.setVal("mqtt", MQTTStatus{Connected: snap.MQTTConnected, Broker: snap.Config.Broker})

	// event_counts: per-channel on/off, profile order.
	var counts orderedObject
	for _, c := range snap.Profile.Channels {
		counts.setVal(c.Key+"_on", snap.GenericCounts[c.OnEvent])
		counts.setVal(c.Key+"_off", snap.GenericCounts[c.OffEvent])
	}
	inner.set("event_counts", mustMarshalJSON(counts))

	if snap.Network != nil {
		inner.setVal("network", NetworkJSON{
			Type:       snap.Network.Type,
			IP:         snap.Network.IP,
			Status:     snap.Network.Status,
			Gateway:    snap.Network.Gateway,
			WifiStatus: snap.Network.WifiStatus,
			SSID:       snap.Network.SSID,
		})
	}

	inner.setVal("config", ConfigJSON{
		PollMs:      snap.Config.PollMs,
		DebounceMs:  snap.Config.DebounceMs,
		HeartbeatMs: snap.Config.HeartbeatMs,
		Broker:      snap.Config.Broker,
		HTTPPort:    snap.Config.HTTPPort,
		WSBroker:    snap.Config.WSBroker,
	})

	return inner
}

func mustMarshalJSON(v interface{}) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

// FormatJSON returns the JSON status for the web endpoint (no event/reason),
// pretty-printed with two-space indentation (matching json.MarshalIndent).
func FormatJSON(snap Snapshot) []byte {
	var root orderedObject
	root.set("status", mustMarshalJSON(buildStatusObject(snap, "", "")))
	compact, _ := json.Marshal(root)
	var out bytes.Buffer
	_ = json.Indent(&out, compact, "", "  ")
	return out.Bytes()
}

// FormatStatusEvent returns the compact JSON status for an MQTT system event.
func FormatStatusEvent(snap Snapshot, event, reason string) []byte {
	var root orderedObject
	root.set("status", mustMarshalJSON(buildStatusObject(snap, event, reason)))
	out, _ := json.Marshal(root)
	return out
}
