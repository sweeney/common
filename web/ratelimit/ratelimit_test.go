package ratelimit_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sweeney/common/web/ratelimit"
	"golang.org/x/time/rate"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func TestRequestsWithinLimitSucceed(t *testing.T) {
	// Allow 5 requests per second with burst of 5
	limiter := ratelimit.NewLimiter(5, 5, "")
	handler := limiter.Middleware(okHandler())

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should succeed", i+1)
	}
}

func TestRequestsExceedingLimitGet429(t *testing.T) {
	// Allow 2 requests per second with burst of 2
	limiter := ratelimit.NewLimiter(2, 2, "")
	handler := limiter.Middleware(okHandler())

	// First 2 should succeed (burst)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should succeed", i+1)
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Verify error body
	var body map[string]string
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "rate_limit_exceeded", body["error"])
}

func TestDifferentIPsHaveIndependentLimits(t *testing.T) {
	// Allow 1 request per second with burst of 1
	limiter := ratelimit.NewLimiter(1, 1, "")
	handler := limiter.Middleware(okHandler())

	// IP 1 uses its one allowed request
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "10.0.0.1:12345"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// IP 1 is now rate limited
	req1b := httptest.NewRequest(http.MethodGet, "/", nil)
	req1b.RemoteAddr = "10.0.0.1:12345"
	w1b := httptest.NewRecorder()
	handler.ServeHTTP(w1b, req1b)
	assert.Equal(t, http.StatusTooManyRequests, w1b.Code)

	// IP 2 should still succeed — independent limit
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.2:12345"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

// TestRetryAfterHeader drives the middleware into a 429 at a range of rates and
// checks the Retry-After header it sends.
//
// Retry-After (RFC 9110 §10.2.3) is a whole number of seconds. The header tells
// a well-behaved client how long to back off, so the value must never be
// shorter than the time it takes the bucket to refill one token (1/rate
// seconds): if it is, the client retries too early and hits another 429.
//
// That is exactly what sweeney/common#1 reported. The header used to be
// formatted with fmt.Sprintf("%.0f", 1/rate), which rounds to nearest (ties to
// even), so every rate >= 2 req/s produced "Retry-After: 0" — an instruction to
// retry immediately. The 2/s and 5/s rows below are the limiters config and
// identity run in production.
//
// The expected value is therefore ceil(1/rate), with a floor of 1 second.
func TestRetryAfterHeader(t *testing.T) {
	tests := []struct {
		name string
		rate float64
		want string
	}{
		// Sub-second refill intervals. These all sent "0" before the fix.
		{name: "identity general limiter, 2/s (1/rate = 0.5s)", rate: 2, want: "1"},
		{name: "config API limiter, 5/s (1/rate = 0.2s)", rate: 5, want: "1"},
		{name: "fast limiter, 100/s (1/rate = 0.01s)", rate: 100, want: "1"},

		// Exactly one second: the boundary between the two regimes.
		{name: "1/s (1/rate = 1s)", rate: 1, want: "1"},

		// Fractional intervals above a second must round UP, not to nearest.
		// 0.4/s is 2.5s; "%.0f" gave "2" (ties to even), too short by half a second.
		{name: "0.4/s (1/rate = 2.5s)", rate: 0.4, want: "3"},
		// 0.3/s is 3.33…s; rounding to nearest gave "3".
		{name: "0.3/s (1/rate = 3.33s)", rate: 0.3, want: "4"},

		// Whole-second intervals are unchanged by the fix.
		{name: "0.5/s (1/rate = 2s), the original test case", rate: 0.5, want: "2"},
		{name: "identity auth limiter, 1 per 12s", rate: float64(rate.Every(12 * time.Second)), want: "12"},

		// A limiter that never refills reports the longest wait it can
		// represent (about 292 years) rather than "+Inf", which is not a valid
		// header value. retryafter_test.go covers this and the other edges.
		{name: "zero rate never refills", rate: 0, want: "9223372036"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Burst 1: the first request takes the only token, the second is denied.
			limiter := ratelimit.NewLimiter(tt.rate, 1, "")
			handler := limiter.Middleware(okHandler())

			w := serve(handler, "10.0.0.1:12345")
			require.Equal(t, http.StatusOK, w.Code, "first request should use the burst token")

			w = serve(handler, "10.0.0.1:12345")
			require.Equal(t, http.StatusTooManyRequests, w.Code, "second request should be rate limited")
			assert.Equal(t, tt.want, w.Header().Get("Retry-After"))
		})
	}
}

// serve sends one GET through handler as if from remoteAddr and returns the
// recorded response.
func serve(handler http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestMiddleware_IgnoresCFConnectingIP_WhenTrustProxyEmpty(t *testing.T) {
	// trustProxy is empty — CF-Connecting-IP should be ignored.
	// All requests should land on the same bucket (RemoteAddr).
	limiter := ratelimit.NewLimiter(1, 1, "")
	handler := limiter.Middleware(okHandler())

	// First request succeeds
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("CF-Connecting-IP", "203.0.113.1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Second request with a different CF-Connecting-IP — should still be rate limited
	// because trustProxy is empty, so we use RemoteAddr (same IP).
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.1:12345"
	req2.Header.Set("CF-Connecting-IP", "203.0.113.2")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code, "should be rate limited because CF-Connecting-IP is ignored")
}

func TestMiddleware_UsesCFConnectingIP_WhenTrustProxyCloudflare(t *testing.T) {
	limiter := ratelimit.NewLimiter(1, 1, "cloudflare")
	handler := limiter.Middleware(okHandler())

	// First request from CF IP 203.0.113.1 succeeds
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("CF-Connecting-IP", "203.0.113.1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Second request from different CF IP should also succeed (different bucket)
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.1:12345"
	req2.Header.Set("CF-Connecting-IP", "203.0.113.2")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code, "different CF IP should have its own bucket")
}

func TestCleanupRemovesStaleEntries(t *testing.T) {
	limiter := ratelimit.NewLimiter(10, 10, "")

	// Make a request to create a visitor
	handler := limiter.Middleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, 1, limiter.VisitorCount())

	// Cleanup with a zero max age should remove the entry
	limiter.Cleanup(0)
	assert.Equal(t, 0, limiter.VisitorCount())
}

func TestVisitorCountTracksMultipleIPs(t *testing.T) {
	limiter := ratelimit.NewLimiter(10, 10, "")
	handler := limiter.Middleware(okHandler())

	ips := []string{"10.0.0.1:1111", "10.0.0.2:2222", "10.0.0.3:3333"}
	for _, ip := range ips {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	assert.Equal(t, 3, limiter.VisitorCount())
}

// TestMaxVisitors_EvictsWhenFull replaces an earlier test that asserted the
// opposite: that a full visitor map denies every unseen IP with a 429.
//
// That was the WP6 defect, not the intended contract. The map is keyed by
// client IP, so whoever can produce distinct keys can fill it — and once full,
// the cap stopped protecting the service and started denying it. The cap now
// bounds memory by evicting the least recently seen visitor, which is what a
// cap is for.
func TestMaxVisitors_EvictsWhenFull(t *testing.T) {
	limiter := ratelimit.NewLimiterWithMaxVisitors(10, 10, "", 3)
	handler := limiter.Middleware(okHandler())

	// Fill up all 3 visitor slots
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.0.%d:12345", i+1)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request from IP %d should succeed", i+1)
	}
	assert.Equal(t, 3, limiter.VisitorCount())

	// A 4th unique IP is served, not denied.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.99:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code,
		"a full visitor map must not deny traffic — that is a self-inflicted outage")

	// And the cap still holds.
	assert.LessOrEqual(t, limiter.VisitorCount(), 3, "the visitor cap must still bound memory")
}

func TestMaxVisitors_EmergencyCleanupFreesSlotsForNewVisitors(t *testing.T) {
	limiter := ratelimit.NewLimiterWithMaxVisitors(10, 10, "", 3)
	handler := limiter.Middleware(okHandler())

	// Fill all 3 slots
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.0.%d:12345", i+1)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	// Age out all entries via cleanup
	limiter.Cleanup(0)
	assert.Equal(t, 0, limiter.VisitorCount())

	// New visitor should now succeed
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.99:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "should allow after cleanup frees slots")
}
