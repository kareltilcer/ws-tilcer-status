// Package mapcap bounds the memory of an in-process keyed map (rate-limiter
// buckets, fixed-window counters) whose key space is attacker-influenced. It
// centralizes the "prune stale entries, then wipe if still over the ceiling"
// guarantee so every limiter shares one memory bound instead of copying it.
package mapcap

// Sweep removes entries for which stale reports true, mutating m in place. If m
// still holds more than maxKeys entries afterward, it is discarded for a fresh
// empty map (bounded degradation under a key-space flood) — so callers must use
// the returned map, which is either m or the replacement:
//
//	l.buckets = mapcap.Sweep(l.buckets, maxKeys, func(b *bucket) bool { ... })
//
// Callers own the throttle (how often to sweep) and the over-cap trigger; Sweep
// only performs the prune-and-cap step they share.
func Sweep[K comparable, V any](m map[K]V, maxKeys int, stale func(V) bool) map[K]V {
	for k, v := range m {
		if stale(v) {
			delete(m, k)
		}
	}
	if len(m) > maxKeys {
		return make(map[K]V)
	}
	return m
}
