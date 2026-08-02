package auth

import (
	"sync"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/mapcap"
)

// maxTrackedKeys caps the number of distinct rate-limit keys held at once. The
// key space is attacker-influenced (clientIP derives from X-Forwarded-For), so
// the sweep below bounds memory under a spoofed-key flood.
const maxTrackedKeys = 4096

// rateLimiter is a small fixed-window counter keyed by an arbitrary string. Login
// uses one per email and one per IP. In-process is sufficient: status runs as a
// single instance.
type rateLimiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	hits      map[string]*hitWindow
	now       func() time.Time
	lastSweep time.Time
}

type hitWindow struct {
	start time.Time
	count int
}

func newRateLimiter(max int, window time.Duration, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{max: max, window: window, hits: map[string]*hitWindow{}, now: now}
}

// allowed reports whether key is currently under the limit, WITHOUT recording an
// attempt. Only failed logins are counted (via fail), so a legitimate user is
// never locked out by their own successful sign-ins.
func (r *rateLimiter) allowed(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.hits[key]
	if w == nil || r.now().Sub(w.start) >= r.window {
		return true
	}
	return w.count < r.max
}

// fail records a failed attempt for key (fixed-window).
func (r *rateLimiter) fail(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.sweep(now)
	w := r.hits[key]
	if w == nil || now.Sub(w.start) >= r.window {
		r.hits[key] = &hitWindow{start: now, count: 1}
		return
	}
	w.count++
}

// sweep drops elapsed windows. Runs at most once per window unless the map is
// over maxTrackedKeys, where it runs eagerly and wipes the map if pruning still
// leaves it over the cap. Callers hold r.mu.
func (r *rateLimiter) sweep(now time.Time) {
	overCap := len(r.hits) > maxTrackedKeys
	if !overCap && now.Sub(r.lastSweep) < r.window {
		return
	}
	r.lastSweep = now
	r.hits = mapcap.Sweep(r.hits, maxTrackedKeys, func(w *hitWindow) bool {
		return now.Sub(w.start) >= r.window
	})
}

// reset clears the counter for key after a successful login.
func (r *rateLimiter) reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hits, key)
}
