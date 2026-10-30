package remoteconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestTrackerRecordsOutcome(t *testing.T) {
	tr := &Tracker{now: fixedClock(t0)}
	tr.Record("sites", nil)
	tr.Record("devices_home", errors.New("boom"))

	got := tr.Statuses()
	want := map[string]Status{
		"sites":        {OK: true, FetchedAt: t0},
		"devices_home": {OK: false, FetchedAt: t0, Error: "boom"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Statuses = %+v, want %+v", got, want)
	}
}

func TestTrackerZeroValueUsable(t *testing.T) {
	var tr Tracker
	if s := tr.Statuses(); len(s) != 0 {
		t.Fatalf("zero Tracker has statuses %v", s)
	}
	if c := tr.Cold("sites"); !reflect.DeepEqual(c, []string{"sites"}) {
		t.Fatalf("zero Tracker Cold = %v", c)
	}
	tr.Record("sites", nil)
	if s := tr.Statuses()["sites"]; !s.OK || s.FetchedAt.IsZero() {
		t.Fatalf("status after Record = %+v", s)
	}
}

func TestTrackerStatusesIsACopy(t *testing.T) {
	var tr Tracker
	tr.Record("sites", nil)
	got := tr.Statuses()
	got["sites"] = Status{Error: "tampered"}
	delete(got, "sites")
	if _, ok := tr.Statuses()["sites"]; !ok {
		t.Fatal("mutating the returned map changed the Tracker")
	}
}

func TestTrackerLandedLatchSurvivesFailure(t *testing.T) {
	var tr Tracker
	tr.Record("devices_home", nil)
	tr.Record("devices_home", errors.New("config down"))

	if c := tr.Cold("devices_home"); len(c) != 0 {
		t.Fatalf("Cold = %v; a namespace that landed once is stale, not cold", c)
	}
	if s := tr.Statuses()["devices_home"]; s.OK {
		t.Fatal("latest status should still report the failure")
	}
}

func TestTrackerColdNeverLanded(t *testing.T) {
	var tr Tracker
	tr.Record("sites", nil)
	tr.Record("floorplan_home", errors.New("404"))

	got := tr.Cold("sites", "floorplan_home", "devices_home", "energy_tariffs")
	want := []string{"devices_home", "energy_tariffs", "floorplan_home"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Cold = %v, want %v (sorted, never-landed only)", got, want)
	}
}

func TestTrackerColdIgnoresUnnamedAndDuplicates(t *testing.T) {
	var tr Tracker
	got := tr.Cold("", "sites", "sites", "")
	if !reflect.DeepEqual(got, []string{"sites"}) {
		t.Fatalf("Cold = %v, want [sites]", got)
	}
}

func TestTrackerColdNoneIsNil(t *testing.T) {
	var tr Tracker
	tr.Record("sites", nil)
	if c := tr.Cold("sites"); c != nil {
		t.Fatalf("Cold = %#v, want nil", c)
	}
	if c := tr.Cold(); c != nil {
		t.Fatalf("Cold() = %#v, want nil", c)
	}
}

// The /healthz wire format of every consumer is built from Status, so its
// JSON is pinned to the bytes the per-service copies produce today.
func TestStatusJSONIsUnchanged(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{Status{OK: true, FetchedAt: t0}, `{"ok":true,"fetched_at":"2026-10-30T09:00:00Z"}`},
		{Status{OK: false, FetchedAt: t0, Error: "boom"}, `{"ok":false,"fetched_at":"2026-10-30T09:00:00Z","error":"boom"}`},
	}
	for _, tc := range cases {
		b, err := json.Marshal(tc.s)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("Marshal = %s, want %s", b, tc.want)
		}
	}
}

func TestTrackerConcurrentUse(t *testing.T) {
	var tr Tracker
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ns := fmt.Sprintf("ns%d", i%3)
			for j := 0; j < 200; j++ {
				if j%2 == 0 {
					tr.Record(ns, nil)
				} else {
					tr.Record(ns, errors.New("x"))
				}
				_ = tr.Statuses()
				_ = tr.Cold("ns0", "ns1", "ns2")
			}
		}(i)
	}
	wg.Wait()
	if c := tr.Cold("ns0", "ns1", "ns2"); c != nil {
		t.Fatalf("Cold = %v after every namespace landed", c)
	}
}
