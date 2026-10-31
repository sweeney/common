package mqttpubtest

import (
	"sync"
	"testing"

	"github.com/sweeney/common/mqttpub"
)

// A daemon publishes from more than one goroutine (a lifecycle loop and a
// work goroutine), while its test polls what has arrived. The methods must
// be safe for that; run with -race.
func TestMethodsAreSafeForConcurrentUse(t *testing.T) {
	var f Fake
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _ = f.Publish(mqttpub.Message{Topic: "a", Retained: true, Payload: "x"}) }()
		go func() {
			defer wg.Done()
			_ = f.All("a")
			_, _ = f.Retained("a")
			_ = f.IndexOf("a")
			_, _ = f.Find("a")
		}()
		go func() { defer wg.Done(); _ = f.Connected(); _ = f.Snapshot() }()
	}
	wg.Wait()
	if got := len(f.Snapshot()); got != 20 {
		t.Errorf("Snapshot has %d messages, want 20", got)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	var f Fake
	_ = f.Publish(mqttpub.Message{Topic: "a"})
	s := f.Snapshot()
	s[0].Topic = "changed"
	if f.Messages[0].Topic != "a" {
		t.Error("Snapshot shares its backing array with Messages")
	}
}
