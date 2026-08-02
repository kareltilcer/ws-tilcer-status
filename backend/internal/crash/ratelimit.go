package crash

import (
	"math"
	"sync"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/mapcap"
)

// maxTrackedKeys bounds the limiter's map. The key space is per-site (a site has
// exactly one active ingest key), so this ceiling is only ever approached under a
// spoofed-site flood, where wiping the map is acceptable degradation.
const maxTrackedKeys = 8192

// ingestLimiter is a per-key token bucket. Ingest is rate-limited per site
// (INGEST_RATE tokens/sec, INGEST_BURST capacity) → 429 with Retry-After.
type ingestLimiter struct {
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

func newIngestLimiter(ratePerSec float64, burst int, now func() time.Time) *ingestLimiter {
	if now == nil {
		now = time.Now
	}
	return &ingestLimiter{
		buckets: map[string]*bucket{},
		rate:    ratePerSec,
		burst:   float64(burst),
		now:     now,
	}
}

// Allow consumes one token for key. When the bucket is empty it returns
// ok=false and the duration until the next token is available (for Retry-After).
func (l *ingestLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
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

// sweep drops full/idle buckets to bound memory. Runs at most once per minute
// unless the map is over the cap, where it wipes to keep memory bounded.
func (l *ingestLimiter) sweep(now time.Time) {
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
