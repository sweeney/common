package ratelimit

// retryafter_test.go covers retryAfterSeconds, which turns the limiter's rate
// into the Retry-After header value sent with a 429 (sweeney/common#1).
//
// The middleware-level table in ratelimit_test.go checks the common rates end
// to end. These tests cover the edges that are awkward or impossible to reach
// through the middleware: floating-point error from rate.Every, and rates that
// are zero, infinite or not a number.

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

// TestRetryAfterSeconds_EveryWholeSecondInterval checks that a limiter built
// with rate.Every(n * time.Second), which is how services usually write "one
// request per n seconds", reports exactly n.
//
// Plain math.Ceil(1/rate) gets this wrong. rate.Every stores the rate as a
// float64 1/n, and inverting that back is not always exact: rate.Every(49s)
// inverts to 49.00000000000001, which math.Ceil turns into 50. Of the 3,600
// whole-second intervals up to an hour, 229 (about 1 in 16) are off by one this
// way. One extra second is harmless to a client, but the header should say
// what the limiter actually does.
func TestRetryAfterSeconds_EveryWholeSecondInterval(t *testing.T) {
	for n := 1; n <= 3600; n++ {
		r := rate.Every(time.Duration(n) * time.Second)
		if got := retryAfterSeconds(r); got != int64(n) {
			t.Errorf("rate.Every(%ds): got Retry-After %d, want %d", n, got, n)
		}
	}
}

// TestRetryAfterSeconds_Edges covers the cases that fall outside "a positive,
// finite rate".
func TestRetryAfterSeconds_Edges(t *testing.T) {
	tests := []struct {
		name string
		rate rate.Limit
		want int64
	}{
		// Rate 0 never refills, so no wait is long enough. The old code sent
		// "+Inf", which is not a valid header value. A plain math.Ceil fix is
		// worse: int(+Inf) is platform-defined in Go (math.MinInt on amd64), so
		// after the floor it sends "1" and tells the client to retry every
		// second, forever. Send the longest wait we represent instead; a
		// client that honours it stops retrying, which is the truth.
		{name: "zero rate never refills", rate: 0, want: maxRetryAfterSeconds},
		{name: "negative rate is treated like zero", rate: -1, want: maxRetryAfterSeconds},
		{name: "NaN rate is treated like zero", rate: rate.Limit(math.NaN()), want: maxRetryAfterSeconds},

		// A tiny positive rate whose refill interval overflows time.Duration
		// (about 292 years) is capped at the same maximum, not wrapped round.
		{name: "interval too long for time.Duration", rate: 1e-12, want: maxRetryAfterSeconds},
		// An interval that fits comfortably (one per 100 years) is not capped.
		{name: "one per 100 years", rate: rate.Every(100 * 365 * 24 * time.Hour), want: 100 * 365 * 24 * 3600},

		// rate.Inf never denies a request, so the middleware never reaches
		// this. If it somehow did, the floor of one second still applies.
		{name: "infinite rate", rate: rate.Inf, want: 1},

		// The smallest positive interval still rounds up to one second.
		{name: "one nanosecond interval", rate: 1e9, want: 1},
		// One nanosecond more than a second must round up to two.
		{name: "just over one second", rate: rate.Every(time.Second + time.Nanosecond), want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, retryAfterSeconds(tt.rate))
		})
	}
}
