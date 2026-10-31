package mqttpub_test

import (
	"testing"
	"time"

	"github.com/sweeney/common/mqttpub"
)

// ups-mqtt stamps its offline announcement, in UTC to the second, the
// shape its FormatOffline produced.
func TestOfflinePayload_Stamped(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 890, time.FixedZone("x", 3600))
	want := `{"online":false,"timestamp":"2026-03-04T04:06:07Z"}`
	if got := mqttpub.OfflinePayload(at); got != want {
		t.Errorf("OfflinePayload = %s, want %s", got, want)
	}
}

// A will is fixed at connect, long before the broker sends it, so netmon
// and tempest-mqtt leave the time out: a zero time gives the bare shape.
func TestOfflinePayload_Unstamped(t *testing.T) {
	if got, want := mqttpub.OfflinePayload(time.Time{}), `{"online":false}`; got != want {
		t.Errorf("OfflinePayload = %s, want %s", got, want)
	}
}
