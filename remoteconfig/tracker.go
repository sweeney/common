package remoteconfig

import (
	"sort"
	"sync"
	"time"
)

// Status is the outcome of the most recent fetch of one namespace, as served
// on a consumer's /healthz. Its JSON is the shape every consumer already
// publishes, so adopting it changes no wire format.
type Status struct {
	OK        bool      `json:"ok"`
	FetchedAt time.Time `json:"fetched_at"`
	Error     string    `json:"error,omitempty"`
}

// Tracker records per-namespace outcomes. The zero value is ready to use and
// it is safe for concurrent use.
type Tracker struct {
	mu       sync.RWMutex
	statuses map[string]Status
	// landed records namespaces that have succeeded at least once. It is not
	// derivable from statuses: a namespace that landed and then failed has
	// OK=false there, and is exactly the case keep-last-good exists for.
	landed map[string]bool

	now func() time.Time // nil: time.Now
}

// Record stores the outcome of a fetch of ns: err == nil is success. Record
// after any validation of your own, so a document that fetched but was
// refused counts as a failure.
//
// A success latches ns as landed for the life of the Tracker. A later failure
// leaves it stale rather than cold: there is a last good document to serve.
func (t *Tracker) Record(ns string, err error) {
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	s := Status{OK: err == nil, FetchedAt: now()}
	if err != nil {
		s.Error = err.Error()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.statuses == nil {
		t.statuses = make(map[string]Status)
		t.landed = make(map[string]bool)
	}
	t.statuses[ns] = s
	if err == nil {
		t.landed[ns] = true
	}
}

// Statuses returns a copy of the latest status of every recorded namespace.
func (t *Tracker) Statuses() map[string]Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Status, len(t.statuses))
	for k, v := range t.statuses {
		out[k] = v
	}
	return out
}

// Cold returns, sorted and without duplicates, those of namespaces that have
// never been recorded as a success, or nil if there are none. An empty name
// is skipped: it names nothing an operator could act on.
//
// Cold is the state keep-last-good cannot cover. A service that refuses to
// boot on a non-empty result, and does not consult it on a reload, gets the
// rule "boot needs truth, running keeps the last truth".
func (t *Tracker) Cold(namespaces ...string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	seen := make(map[string]bool, len(namespaces))
	var out []string
	for _, ns := range namespaces {
		if ns == "" || seen[ns] || t.landed[ns] {
			continue
		}
		seen[ns] = true
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}
