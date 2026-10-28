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

// Visitor cleanup timings. A visitor idle for longer than visitorIdleTimeout
// is dropped at the next cleanup tick, and its next request starts with a
// fresh, full bucket. retryAfterSeconds relies on this to cap Retry-After.
const (
	visitorIdleTimeout = 3 * time.Minute
	cleanupInterval    = time.Minute
)

// cleanupLoop removes visitors that haven't been seen for visitorIdleTimeout.
func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		l.Cleanup(visitorIdleTimeout)
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
// whole seconds rounded up, between 1 and maxRetryAfterSeconds (241). See
// retryAfterSeconds.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httputil.ExtractClientIP(r, l.trustProxy)
		limiter := l.getVisitor(ip)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(l.rate)))
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

// retryAfterMargin is added to the cleanup horizon to allow for a late ticker
// tick or a retry that races the tick that would have reset it.
const retryAfterMargin = time.Second

// maxRetryAfterSeconds caps Retry-After at the cleanup horizon: the longest an
// idle client can wait before cleanup drops its visitor and resets its bucket
// (the idle timeout, plus up to one tick for cleanup to notice, plus the
// margin). That is 241 seconds. Waiting any longer than this gains nothing,
// whatever the rate. A small cap also keeps the value safe for clients that
// overflow on large delays (e.g. JavaScript setTimeout beyond about 24.8 days).
const maxRetryAfterSeconds = int((visitorIdleTimeout + cleanupInterval + retryAfterMargin) / time.Second)

// retryAfterSeconds returns the Retry-After value, in whole seconds, sent with
// a 429 for a limiter refilling at rate r.
//
// The value is the time to refill one token, 1/r, rounded up, never less than
// 1 and never more than maxRetryAfterSeconds. Rounding down or to nearest would
// tell clients to retry before a token is available, into another 429; 0 would
// tell them to retry at once.
//
// The interval is truncated to whole nanoseconds before rounding up in integer
// arithmetic. Rounding 1/r as a float is off by one for many rate.Every
// intervals: 1/(1/49) is 49.00000000000001, which math.Ceil makes 50.
//
// Rates that are zero, negative or NaN never refill the bucket, so they get the
// cap: cleanup is what lets those clients back in. rate.Inf never denies, so the
// middleware never asks for it; it would get the 1-second floor.
func retryAfterSeconds(r rate.Limit) int {
	// NaN fails every comparison, so test for it explicitly.
	if r <= 0 || math.IsNaN(float64(r)) {
		return maxRetryAfterSeconds
	}

	// The refill interval in nanoseconds. Comparing against the cap before
	// converting also keeps the float-to-Duration conversion in range: an
	// out-of-range conversion is platform-defined in Go.
	ns := float64(time.Second) / float64(r)
	if ns >= float64(time.Duration(maxRetryAfterSeconds)*time.Second) {
		return maxRetryAfterSeconds
	}
	interval := time.Duration(ns) // truncates to whole nanoseconds

	// Ceiling division to whole seconds, with a floor of 1.
	secs := int(interval / time.Second)
	if interval%time.Second != 0 {
		secs++
	}
	return max(secs, 1)
}
