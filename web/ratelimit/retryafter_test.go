package ratelimit

// Tests for retryAfterSeconds and the Retry-After header it produces. The
// common rates are covered end to end in ratelimit_test.go; these cover the
// edges and check the header against recovery through visitor cleanup.

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// A limiter built with rate.Every(n * time.Second) must report exactly n (up
// to the cap). Float math.Ceil(1/r) gets 229 of these wrong: 1/(1/49) is
// 49.00000000000001, which rounds up to 50.
func TestRetryAfterSeconds_EveryWholeSecondInterval(t *testing.T) {
	for n := 1; n <= 3600; n++ {
		r := rate.Every(time.Duration(n) * time.Second)
		want := min(n, maxRetryAfterSeconds)
		if got := retryAfterSeconds(r); got != want {
			t.Errorf("rate.Every(%ds): got Retry-After %d, want %d", n, got, want)
		}
	}
}

func TestRetryAfterSeconds_Edges(t *testing.T) {
	tests := []struct {
		name string
		rate rate.Limit
		want int
	}{
		// The bucket never refills, but cleanup resets it, so these wait for
		// cleanup like any other slow limiter.
		{name: "zero rate", rate: 0, want: maxRetryAfterSeconds},
		{name: "negative rate", rate: -1, want: maxRetryAfterSeconds},
		{name: "NaN rate", rate: rate.Limit(math.NaN()), want: maxRetryAfterSeconds},

		// Refill intervals at, just under and beyond the cap.
		{name: "interval one second under the cap", rate: rate.Every(time.Duration(maxRetryAfterSeconds-1) * time.Second), want: maxRetryAfterSeconds - 1},
		{name: "interval equal to the cap", rate: rate.Every(time.Duration(maxRetryAfterSeconds) * time.Second), want: maxRetryAfterSeconds},
		{name: "one per day", rate: rate.Every(24 * time.Hour), want: maxRetryAfterSeconds},
		{name: "interval too long for time.Duration", rate: 1e-12, want: maxRetryAfterSeconds},

		// rate.Inf never denies, so the middleware never asks; it would get the floor.
		{name: "infinite rate", rate: rate.Inf, want: 1},
		{name: "one nanosecond interval", rate: 1e9, want: 1},
		{name: "just over one second", rate: rate.Every(time.Second + time.Nanosecond), want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, retryAfterSeconds(tt.rate))
		})
	}
}

// TestRetryAfter_MatchesRecoveryAfterCleanup checks the header against what
// really happens to a client that obeys it, for limiters that refill slower
// than visitor cleanup runs.
//
// A bucket that refills slowly, or never (rate 0), is not the whole story: the
// cleanup loop drops a visitor idle for longer than the idle timeout, and the
// next request from that IP gets a fresh bucket with a full burst. So an
// obedient client is let back in once cleanup has run, whatever the rate. This
// test simulates the cleanup ticker at every phase (to the second) relative to
// the denied request, finds when the visitor is actually dropped, and checks:
//
//   - a request sent after waiting Retry-After seconds succeeds, at every phase;
//   - Retry-After is exactly the worst case plus retryAfterMargin, so clients
//     are not told to wait longer than they need to.
func TestRetryAfter_MatchesRecoveryAfterCleanup(t *testing.T) {
	idle, tick := visitorIdleTimeout, cleanupInterval

	rates := []struct {
		name string
		rate rate.Limit
	}{
		{name: "zero rate", rate: 0},
		{name: "one per 10 minutes", rate: rate.Every(10 * time.Minute)},
		{name: "one per day", rate: rate.Every(24 * time.Hour)},
		{name: "interval too long for time.Duration", rate: 1e-12},
	}

	for _, tt := range rates {
		t.Run(tt.name, func(t *testing.T) {
			var worst time.Duration // longest wait any ticker phase needed

			for phase := time.Duration(0); phase < tick; phase += time.Second {
				start := time.Unix(1_000_000, 0)
				now := start
				l := &Limiter{
					visitors:    make(map[string]*visitor),
					rate:        tt.rate,
					burst:       1,
					maxVisitors: 100,
					now:         func() time.Time { return now },
				}
				handler := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				send := func() *httptest.ResponseRecorder {
					req := httptest.NewRequest(http.MethodGet, "/", nil)
					req.RemoteAddr = "10.0.0.1:12345"
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					return w
				}

				require.Equal(t, http.StatusOK, send().Code, "burst token")
				denied := send() // the visitor is last seen now, at start
				require.Equal(t, http.StatusTooManyRequests, denied.Code)
				secs, err := strconv.ParseInt(denied.Header().Get("Retry-After"), 10, 64)
				require.NoError(t, err)
				wait := time.Duration(secs) * time.Second

				// Run the cleanup ticker, which fires at start+phase, then every
				// tick, until it drops the visitor or the client's wait is over.
				for at := start.Add(phase); !at.After(start.Add(wait)); at = at.Add(tick) {
					now = at
					l.Cleanup(idle)
					if l.VisitorCount() == 0 {
						worst = max(worst, at.Sub(start))
						break
					}
				}

				now = start.Add(wait)
				require.Equal(t, http.StatusOK, send().Code,
					"phase %v: a client that waited Retry-After (%ds) must be let back in", phase, secs)
			}

			want := worst + retryAfterMargin
			assert.Equal(t, int(want/time.Second), retryAfterSeconds(tt.rate),
				"Retry-After should be the worst-case recovery (%v) plus the margin", worst)
		})
	}
}
