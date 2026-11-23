// Package serve is what a read-only JSON API needs around its handlers:
// the response writers, CORS for browser consumers, and an http.Server
// with timeouts that shuts down gracefully. Stdlib only.
package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"
)

// ShutdownWait is how long Run lets requests in flight finish.
const ShutdownWait = 5 * time.Second

// WriteJSON writes v as JSON with status. It encodes first, so a value
// encoding/json can't marshal (a NaN, say) is a 500 with the JSON error,
// never a 200 with a broken body. HTML isn't escaped: this is an API.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := enc.Encode(v); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("{\"error\":\"could not encode the response\"}\n"))
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// WriteError writes the error envelope, {"error": msg}, with status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// CORS lets browser consumers on any origin (file:// included) call a
// read-only API: a wildcard Access-Control-Allow-Origin, and preflights
// answered here, before the handler and so before auth (a preflight
// carries no Authorization header). The wildcard is safe because the API
// authenticates with Bearer tokens, not cookies; no credentials are
// allowed. Timing-Allow-Origin, which CORS doesn't imply, lets a consumer
// see its requests' timings; a page without a token can time only its own
// 401.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Timing-Allow-Origin", "*")
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewServer is an http.Server for h with the timeouts every service uses.
func NewServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Run serves h on ln until ctx is done, then shuts down, letting requests
// in flight finish for up to ShutdownWait. It returns nil after a
// shutdown, or the error that stopped the server.
func Run(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := NewServer(h)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), ShutdownWait)
	defer cancel()
	err := srv.Shutdown(sctx)
	if serr := <-errc; !errors.Is(serr, http.ErrServerClosed) && err == nil {
		err = serr
	}
	return err
}
