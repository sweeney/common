package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var start = time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC)

func fixedRuntime() Runtime { return Runtime{Goroutines: 12, HeapMB: 2.34, SysMB: 9.87} }

func TestReportJSONOrderAndRounding(t *testing.T) {
	r := Report{
		Status:  StatusDegraded,
		Detail:  "MQTT disconnected",
		Version: "abc1234",
		Start:   start,
		Now:     start.Add(90*time.Second + 400*time.Millisecond),
		Runtime: fixedRuntime(),
		Fields: []Field{
			{"ready", true},
			{"tanks", []string{"a", "b"}},
		},
	}
	got, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"status":"degraded","detail":"MQTT disconnected","version":"abc1234","uptime_s":90,"start_time":"2026-11-02T21:00:00Z","goroutines":12,"heap_mb":2.3,"sys_mb":9.9,"ready":true,"tanks":["a","b"]}`
	if string(got) != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestDetailOmittedWhenEmpty(t *testing.T) {
	got, _ := json.Marshal(Report{Status: StatusOK, Version: "v", Start: start, Now: start, Runtime: fixedRuntime()})
	if strings.Contains(string(got), "detail") {
		t.Errorf("detail should be omitted when empty: %s", got)
	}
}

func TestAFieldCannotShadowASharedOne(t *testing.T) {
	_, err := json.Marshal(Report{Status: StatusOK, Start: start, Now: start, Fields: []Field{{"status", "fine"}}})
	if err == nil {
		t.Error("a service field named like a shared one must be refused, not emitted twice")
	}
}

func TestHandlerAlways200(t *testing.T) {
	for _, status := range []string{StatusOK, StatusStarting, StatusDegraded} {
		h := Handler(func() Report {
			return Report{Status: status, Version: "v", Start: start, Now: start.Add(time.Second), Runtime: fixedRuntime()}
		})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: code %d, want 200 (the body carries the state)", status, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("content type %q", ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("cache control %q", cc)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["status"] != status {
			t.Errorf("body %s (%v)", rec.Body.String(), err)
		}
		if !strings.Contains(rec.Body.String(), "\n  \"status\"") {
			t.Errorf("want two-space indented output for curl readers: %s", rec.Body.String())
		}
	}
}

func TestHandlerFillsRuntimeAndNow(t *testing.T) {
	h := Handler(func() Report { return Report{Status: StatusOK, Version: "v", Start: time.Now().Add(-time.Minute)} })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body struct {
		UptimeS    int64 `json:"uptime_s"`
		Goroutines int   `json:"goroutines"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.UptimeS < 59 || body.Goroutines < 1 {
		t.Errorf("body %s", rec.Body.String())
	}
}

func TestReadRuntime(t *testing.T) {
	r := ReadRuntime()
	if r.Goroutines < 1 || r.SysMB <= 0 {
		t.Errorf("runtime = %+v", r)
	}
}
