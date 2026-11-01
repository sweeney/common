// Package health is the /healthz shared by Sweeney's Pi services. It always
// answers 200: the status field carries the state, so a monitor reads one
// thing rather than guessing from a code. The shared fields come first, in a
// fixed order, so a person reading it with curl sees status and version
// first; each service's own fields follow, in the order it gives them.
package health

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"time"
)

// Statuses.
const (
	StatusOK       = "ok"       // working normally
	StatusStarting = "starting" // running, but nothing real to report yet
	StatusDegraded = "degraded" // running, but something needs attention; Detail says what
)

// Runtime is the process's own numbers: a goroutine leak shows here long
// before RSS does, so "is it leaking?" is one curl rather than an ssh.
type Runtime struct {
	Goroutines int
	HeapMB     float64
	SysMB      float64
}

// ReadRuntime reads the current process's numbers.
func ReadRuntime() Runtime {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return Runtime{
		Goroutines: runtime.NumGoroutine(),
		HeapMB:     float64(m.HeapAlloc) / (1 << 20),
		SysMB:      float64(m.Sys) / (1 << 20),
	}
}

// Field is one service-specific field, emitted after the shared ones.
type Field struct {
	Key   string
	Value any
}

// Report is one /healthz body.
type Report struct {
	Status  string
	Detail  string // omitted when empty
	Version string
	Start   time.Time
	Now     time.Time // for uptime; Handler fills it if zero
	Runtime Runtime   // Handler fills it if zero
	Fields  []Field
}

var shared = map[string]bool{
	"status": true, "detail": true, "version": true, "uptime_s": true,
	"start_time": true, "goroutines": true, "heap_mb": true, "sys_mb": true,
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// MarshalJSON writes the shared fields in their fixed order, then Fields.
func (r Report) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	n := 0
	put := func(k string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("health: field %q: %w", k, err)
		}
		if n > 0 {
			buf.WriteByte(',')
		}
		n++
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(b)
		return nil
	}
	_ = put("status", r.Status)
	if r.Detail != "" {
		_ = put("detail", r.Detail)
	}
	_ = put("version", r.Version)
	_ = put("uptime_s", int64(r.Now.Sub(r.Start)/time.Second))
	_ = put("start_time", r.Start.UTC().Format(time.RFC3339))
	_ = put("goroutines", r.Runtime.Goroutines)
	_ = put("heap_mb", round1(r.Runtime.HeapMB))
	_ = put("sys_mb", round1(r.Runtime.SysMB))
	for _, f := range r.Fields {
		if shared[f.Key] {
			return nil, fmt.Errorf("health: field %q would shadow a shared field", f.Key)
		}
		if err := put(f.Key, f.Value); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Handler serves report() as /healthz: always 200, JSON, two-space
// indented, never cached.
func Handler(report func() Report) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r := report()
		if r.Now.IsZero() {
			r.Now = time.Now()
		}
		if r.Runtime == (Runtime{}) {
			r.Runtime = ReadRuntime()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		b, err := json.Marshal(r)
		if err != nil {
			// A service bug, not a health state: still 200 with a body that
			// says so, so a monitor sees "degraded" rather than a broken page.
			b, _ = json.Marshal(map[string]string{"status": StatusDegraded, "detail": err.Error(), "version": r.Version})
		}
		var out bytes.Buffer
		_ = json.Indent(&out, b, "", "  ")
		out.WriteByte('\n')
		_, _ = w.Write(out.Bytes())
	})
}
