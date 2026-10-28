package ratelimit

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/sweeney/common/web/httputil"
	"golang.org/x/time/rate"
)

// visitor tracks the rate limiter and last-seen time for a single IP.
type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter provides per-IP rate limiting middleware.
type Limiter struct {
	mu          sync.Mutex
	visitors    map[string]*visitor
	rate        rate.Limit
	burst       int
	trustProxy  string
	maxVisitors int
	now         func() time.Time // for testing
}

// NewLimiter creates a rate limiter with a default max of 100,000 tracked visitors.
// r is the rate in requests per second, burst is the maximum burst size.
// trustProxy controls whether proxy headers (e.g. CF-Connecting-IP) are trusted for IP extraction.
// Stale entries are cleaned up every 3 minutes.
func NewLimiter(r float64, burst int, trustProxy string) *Limiter {
	return NewLimiterWithMaxVisitors(r, burst, trustProxy, 100000)
}

// NewLimiterWithMaxVisitors creates a rate limiter with a configurable max visitor cap.
func NewLimiterWithMaxVisitors(r float64, burst int, trustProxy string, maxVisitors int) *Limiter {
	l := &Limiter{
		visitors:    make(map[string]*visitor),
		rate:        rate.Limit(r),
		burst:       burst,
		trustProxy:  trustProxy,
		maxVisitors: maxVisitors,
		now:         time.Now,
	}
	go l.cleanupLoop()
	return l
}

// getVisitor returns the rate limiter for the given IP, creating one if needed.
func (l *Limiter) getVisitor(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	v, exists := l.visitors[ip]
	if exists {
		v.lastSeen = l.now()
		return v.limiter
	}

	// Enforce max visitors.
	if len(l.visitors) >= l.maxVisitors {
		// Emergency cleanup — remove entries older than 30 seconds.
		l.cleanupLocked(30 * time.Second)
	}
	// Still full: evict the least recently seen entry to make room. This used
	// to return a deny-all limiter to every unseen IP instead, which turns a
	// full table into a service-wide outage — and the table is fillable by
	// whoever can produce distinct keys, so the cap became a denial-of-service
	// lever rather than a safeguard. A full table means we are tracking too
	// many visitors, not that the next visitor is hostile.
	for len(l.visitors) >= l.maxVisitors {
		if !l.evictOldestLocked() {
			break
		}
	}

	limiter := rate.NewLimiter(l.rate, l.burst)
	l.visitors[ip] = &visitor{limiter: limiter, lastSeen: l.now()}
	return limiter
}

// evictOldestLocked removes the least recently seen visitor. Returns false when
// there is nothing to evict. Caller must hold l.mu.
func (l *Limiter) evictOldestLocked() bool {
	var oldestIP string
	var oldestSeen time.Time
	for ip, v := range l.visitors {
		if oldestIP == "" || v.lastSeen.Before(oldestSeen) {
			oldestIP, oldestSeen = ip, v.lastSeen
		}
	}
	if oldestIP == "" {
		return false
	}
	delete(l.visitors, oldestIP)
	return true
}

// cleanupLoop removes visitors that haven't been seen in the last 3 minutes.
func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.Cleanup(3 * time.Minute)
	}
}

// Cleanup removes entries older than maxAge.
func (l *Limiter) Cleanup(maxAge time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(maxAge)
}

// cleanupLocked removes entries older than maxAge. Caller must hold l.mu.
func (l *Limiter) cleanupLocked(maxAge time.Duration) {
	now := l.now()
	for ip, v := range l.visitors {
		if now.Sub(v.lastSeen) > maxAge {
			delete(l.visitors, ip)
		}
	}
}

// VisitorCount returns the number of tracked IPs. For testing/monitoring.
func (l *Limiter) VisitorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.visitors)
}

// Middleware returns an http.Handler that rate-limits by client IP.
//
// A request over the limit gets 429 Too Many Requests with a JSON error body
// and a Retry-After header: the time for the bucket to refill one token, in
// whole seconds rounded up, never less than 1. See retryAfterSeconds.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httputil.ExtractClientIP(r, l.trustProxy)
		limiter := l.getVisitor(ip)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(l.rate), 10))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "rate_limit_exceeded",
				"message": "Too many requests. Please try again later.",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// maxRetryAfterSeconds is the longest Retry-After the middleware sends: the
// longest interval a time.Duration can hold, about 292 years. It is what a
// limiter that never refills (rate 0) reports, and the cap for rates so slow
// their refill interval would overflow time.Duration.
const maxRetryAfterSeconds = int64(math.MaxInt64 / int64(time.Second))

// retryAfterSeconds returns the Retry-After value, in whole seconds, that the
// middleware sends with a 429 for a limiter refilling at rate r.
//
// Retry-After (RFC 9110 §10.2.3) is a whole number of seconds telling the
// client how long to wait. A denied request has found its bucket empty, and the
// bucket gains one token every 1/r seconds, so that is the shortest useful
// wait. The result is 1/r rounded UP, and never less than 1:
//
//   - Rounding to nearest, as this used to do with fmt.Sprintf("%.0f", 1/r),
//     sends "0" for every r >= 2 req/s, telling clients to retry at once into
//     another 429 (sweeney/common#1). It also under-reports intervals such as
//     2.5s as "2".
//   - A value of 0 is never sent: any interval under a second still means
//     "not yet", and 1 is the shortest wait the header can express.
//
// The interval is first truncated to whole nanoseconds, then rounded up to
// whole seconds in integer arithmetic. Doing the rounding on 1/r as a float
// would be wrong: rate.Every(49*time.Second) stores 1/49, and 1/(1/49) is
// 49.00000000000001 in float64, which math.Ceil turns into 50. Truncating to a
// nanosecond first discards that rounding noise. It could only under-report if
// the true interval were a whole number of seconds plus less than a
// nanosecond, which no real limiter configuration is.
//
// Rates that are zero, negative or NaN never refill (rate.Limiter treats r <= 0
// as "no new tokens"), so they get maxRetryAfterSeconds, as do positive rates
// so small the interval overflows time.Duration. rate.Inf never denies, so the
// middleware does not call this for it; it would get the 1-second floor.
func retryAfterSeconds(r rate.Limit) int64 {
	// NaN fails every comparison, so test for it explicitly alongside r <= 0.
	if r <= 0 || math.IsNaN(float64(r)) {
		return maxRetryAfterSeconds
	}

	// The refill interval in nanoseconds, as a float. Check it fits in a
	// time.Duration before converting: an out-of-range float-to-int conversion
	// is platform-defined in Go. float64(math.MaxInt64) rounds up to 2^63, so
	// ">=" rejects exactly the values that do not fit.
	ns := float64(time.Second) / float64(r)
	if ns >= float64(math.MaxInt64) {
		return maxRetryAfterSeconds
	}
	interval := time.Duration(ns) // truncates to whole nanoseconds

	// Ceiling division: whole seconds, rounding any remainder up. The interval
	// is at most MaxInt64, so add the remainder rather than (interval +
	// time.Second - 1), which could overflow.
	secs := int64(interval / time.Second)
	if interval%time.Second != 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	return secs
}
