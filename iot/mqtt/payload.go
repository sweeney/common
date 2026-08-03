package mqtt

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/profile"
)

// FormatEventPayload builds the event JSON for an arbitrary sensor profile.
//
// It serializes in the profile's channel order using an explicit ordered
// encoder rather than json.Marshal of a map[string]ChannelState — Go sorts map
// keys alphabetically, which would reorder/break the wire contract. The profile
// defines channel ordering and this honours it exactly.
//
// Output shape:
//
//	{"<root>":{"timestamp":"...","event":"<name>","<key1>":{"state":"ON"},...}}
func FormatEventPayload(p profile.SensorProfile, timestamp time.Time, eventName string, states map[string]logic.State) []byte {
	var b bytes.Buffer
	b.WriteString(`{`)
	b.Write(jsonString(p.PayloadRoot))
	b.WriteString(`:{`)
	b.WriteString(`"timestamp":`)
	b.Write(jsonString(timestamp.UTC().Format(time.RFC3339)))
	b.WriteString(`,"event":`)
	b.Write(jsonString(eventName))
	for _, c := range p.Channels {
		b.WriteString(`,`)
		b.Write(jsonString(c.Key))
		b.WriteString(`:{"state":`)
		b.Write(jsonString(string(states[c.Key])))
		b.WriteString(`}`)
	}
	b.WriteString(`}}`)
	return b.Bytes()
}

// jsonString returns the JSON-encoded (quoted, escaped) form of s.
func jsonString(s string) []byte {
	out, _ := json.Marshal(s)
	return out
}
