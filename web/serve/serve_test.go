package serve

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusCreated, map[string]string{"url": "a<b>&c"})
	if w.Code != http.StatusCreated || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("code %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if got := w.Body.String(); got != "{\"url\":\"a<b>&c\"}\n" {
		t.Errorf("body %q (HTML isn't escaped)", got)
	}
}

// A value encoding/json can't marshal is a real 500 with the JSON error,
// never a 200 with a broken body.
func TestWriteJSONThatCannotEncodeIsA500(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusOK, map[string]float64{"x": math.NaN()})
	if w.Code != http.StatusInternalServerError || w.Body.String() != "{\"error\":\"could not encode the response\"}\n" {
		t.Errorf("code %d, body %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("type %q", w.Header().Get("Content-Type"))
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusNotFound, "no tank \"x\"")
	if w.Code != http.StatusNotFound || w.Body.String() != "{\"error\":\"no tank \\\"x\\\"\"}\n" {
		t.Errorf("code %d, body %q", w.Code, w.Body.String())
	}
}

func TestCORS(t *testing.T) {
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "*" ||
		w.Header().Get("Timing-Allow-Origin") != "*" || w.Header().Get("Vary") != "Origin" {
		t.Errorf("GET: %d %v", w.Code, w.Header())
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials allowed")
	}

	// A preflight is answered before the handler (and so before auth: it
	// carries no Authorization header).
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/x", nil))
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Methods") != "GET, OPTIONS" ||
		w.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" ||
		w.Header().Get("Access-Control-Max-Age") != "600" {
		t.Errorf("OPTIONS: %d %v", w.Code, w.Header())
	}
}

func TestRunServesUntilCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hi") }))
	}()
	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "hi" {
		t.Errorf("body %q", b)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return")
	}
	if _, err := http.Get("http://" + ln.Addr().String() + "/"); err == nil {
		t.Error("still serving after Run returned")
	}
}

// A request in flight when ctx is cancelled finishes.
func TestRunFinishesARequestInFlight(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = io.WriteString(w, "late")
		}))
	}()
	got := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			got <- err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		got <- string(b)
	}()
	<-started
	cancel()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if s := <-got; s != "late" {
		t.Errorf("in flight: %q", s)
	}
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestRunReportsAServeError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if err := Run(context.Background(), ln, http.NotFoundHandler()); err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Errorf("got %v", err)
	}
}

func TestNewServerTimeouts(t *testing.T) {
	s := NewServer(http.NotFoundHandler())
	if s.ReadHeaderTimeout != 5*time.Second || s.ReadTimeout != 10*time.Second ||
		s.WriteTimeout != 30*time.Second || s.IdleTimeout != 60*time.Second {
		t.Errorf("%+v", s)
	}
}

// A request still running when the shutdown's wait is up is cut off: Run
// closes the server, and returns the shutdown's error.
func TestRunClosesWhenTheShutdownRunsOver(t *testing.T) {
	old := shutdownWait
	shutdownWait = 50 * time.Millisecond
	defer func() { shutdownWait = old }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
		}))
	}()
	got := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
		got <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return")
	}
	select {
	case err := <-got:
		if err == nil {
			t.Error("the request in flight wasn't cut off")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request in flight is still open")
	}
}
