package mqttpub

import (
	"encoding/json"
	"time"
)

// offline is the {"online":false} shape shared by the fleet's state topics,
// as their last will and their clean-shutdown announcement.
type offline struct {
	Online    bool   `json:"online"`
	Timestamp string `json:"timestamp,omitempty"`
}

// OfflinePayload returns the {"online":false} payload, stamped with at in
// UTC to the second (RFC 3339).  A zero at leaves the timestamp out, the
// shape of netmon's and tempest-mqtt's last will; ups-mqtt stamps both its
// will and its shutdown announcement with time.Now().  It is an optional
// helper: a service with its own offline shape keeps it.
func OfflinePayload(at time.Time) string {
	o := offline{}
	if !at.IsZero() {
		o.Timestamp = at.UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(o)
	return string(b)
}
