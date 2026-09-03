// Package ratelimit is the token bucket the service's key-authenticated public
// endpoints share: crash ingest per site, feedback submission per widget key and
// per client IP. One implementation, so a limit cannot behave differently
// depending on which endpoint it guards.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/mapcap"
)

// maxTrackedKeys bounds a limiter's map. The key space is a site id, a widget
// key hash or a client IP, so this ceiling is only ever approached under a
// spoofed-key flood, where wiping the map is acceptable degradation.
const maxTrackedKeys = 8192

// Limiter is a per-key token bucket: rate tokens/sec accruing to a burst
// capacity, one token per request → 429 with Retry-After when empty.
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // tokens per second
	burst     float64 // bucket capacity
	now       func() time.Time
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a limiter. now may be nil (time.Now); tests inject a clock.
func New(ratePerSec float64, burst int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		buckets: map[string]*bucket{},
		rate:    ratePerSec,
		burst:   float64(burst),
		now:     now,
	}
}

// Allow consumes one token for key. When the bucket is empty it returns
// ok=false and the duration until the next token is available (for Retry-After).
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	// Refill based on elapsed time, capped at burst.
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0
	}
	// Time until one more token accrues.
	need := (1 - b.tokens) / l.rate
	return false, time.Duration(need * float64(time.Second))
}

// Refund returns one token to key's bucket, never taking it above burst.
//
// ⚠ It exists for the one situation a single bucket cannot express: a request
// that must satisfy TWO limiters. `Allow` decides and charges in the same call,
// so whichever bucket is consulted first has already paid when the second one
// refuses — and if the first bucket is shared (a site's whole budget) and the
// second is the caller's own (their IP), one refused caller spends everybody
// else's allowance. The caller charges the narrowest bucket first and refunds it
// when a later one says no.
//
// Refunding a bucket that has since refilled is a no-op rather than an
// over-credit, and refunding a key that was swept is silently dropped: a fresh
// bucket already starts full, so there is nothing to give back.
func (l *Limiter) Refund(key string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		return
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	b.tokens = math.Min(l.burst, b.tokens+1)
}

// sweep drops full/idle buckets to bound memory. Runs at most once per minute
// unless the map is over the cap, where it wipes to keep memory bounded.
func (l *Limiter) sweep(now time.Time) {
	overCap := len(l.buckets) > maxTrackedKeys
	if !overCap && now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	l.buckets = mapcap.Sweep(l.buckets, maxTrackedKeys, func(b *bucket) bool {
		// A bucket that has fully refilled is indistinguishable from a fresh one.
		return b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst
	})
}
