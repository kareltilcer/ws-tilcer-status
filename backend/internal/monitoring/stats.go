package monitoring

import (
	"math"
	"sort"
)

// percentile returns the p-th percentile (nearest-rank) of xs. p is 1..100. The
// bool is false when xs is empty. SQLite has no percentile function, so daily
// rollups compute p50/p95 here from that day's latency samples.
func percentile(xs []int, p int) (int, bool) {
	n := len(xs)
	if n == 0 {
		return 0, false
	}
	sorted := append([]int(nil), xs...)
	sort.Ints(sorted)
	rank := int(math.Ceil(float64(p) / 100.0 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1], true
}

// weightedMean returns the value-weighted mean of vals weighted by weights,
// rounded to the nearest int, and whether any weight was positive. Used to
// approximate an overall latency percentile from daily percentiles (an exact
// percentile cannot be reconstructed from per-day percentiles).
func weightedMean(vals []int, weights []int) (int, bool) {
	var sum, wsum float64
	for i := range vals {
		w := float64(weights[i])
		sum += float64(vals[i]) * w
		wsum += w
	}
	if wsum == 0 {
		return 0, false
	}
	return int(math.Round(sum / wsum)), true
}
