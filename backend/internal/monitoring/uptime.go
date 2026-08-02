package monitoring

import (
	"context"
	"database/sql"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// windowDuration maps the window enum to a span (default 90d).
func windowDuration(w string) time.Duration {
	switch w {
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default: // "90d"
		return 90 * 24 * time.Hour
	}
}

// Uptime composes an UptimeSummary for a site. For 7d/30d/90d it reads only
// check_rollup (never a raw scan). For 24h — which daily rollups cannot render —
// it does a single bounded raw scan (≤ ~288 rows/day) for an exact intraday
// strip. A bucket with no checks reports ok_pct: null (a gap), distinct from 0.
// found is false when the site does not exist.
func (m *Module) Uptime(ctx context.Context, siteID, window string, buckets int, now time.Time) (*UptimeSummary, bool, error) {
	enabled, found, err := m.store.SiteMeta(ctx, siteID)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	if buckets < 1 {
		buckets = 1
	}
	if buckets > 180 {
		buckets = 180
	}

	dur := windowDuration(window)
	nowUTC := now.UTC()
	to := nowUTC
	from := to.Add(-dur)
	if window != "24h" {
		// Daily rollups are keyed by UTC calendar day (midnight-aligned). Snap the
		// bucket grid to whole UTC days ending with today so every bucket boundary
		// falls on midnight; otherwise, at any non-midnight request time, a day's
		// rollup lands in a neighbouring bucket and the per-day strip and its date
		// labels are off by up to a day (the overall totals are unaffected). `to`
		// becomes the start of tomorrow — the exclusive upper bound that folds all
		// of today into the final bucket.
		todayStart := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
		windowDays := int(dur / (24 * time.Hour))
		to = todayStart.AddDate(0, 0, 1)
		from = to.AddDate(0, 0, -windowDays)
	}

	sum := &UptimeSummary{
		Window:  window,
		From:    timeutil.Format(from),
		To:      timeutil.Format(to),
		Buckets: emptyBuckets(from, to, buckets),
	}
	// Monitoring off: no availability to report — null uptime, all-gap buckets.
	if !enabled {
		return sum, true, nil
	}
	if window == "24h" {
		if err := m.uptimeFromRaw(ctx, siteID, from, to, buckets, sum); err != nil {
			return nil, true, err
		}
	} else {
		if err := m.uptimeFromRollups(ctx, siteID, from, to, nowUTC, buckets, sum); err != nil {
			return nil, true, err
		}
	}
	return sum, true, nil
}

func emptyBuckets(from, to time.Time, n int) []UptimeBucket {
	dur := to.Sub(from)
	bucketDur := dur / time.Duration(n)
	out := make([]UptimeBucket, n)
	for i := 0; i < n; i++ {
		start := from.Add(time.Duration(i) * bucketDur)
		end := to
		if i < n-1 {
			end = from.Add(time.Duration(i+1) * bucketDur)
		}
		out[i] = UptimeBucket{Start: timeutil.Format(start), End: timeutil.Format(end)}
	}
	return out
}

func bucketIndex(t, from time.Time, bucketDur time.Duration, n int) int {
	if bucketDur <= 0 {
		return 0
	}
	idx := int(t.Sub(from) / bucketDur)
	if idx < 0 {
		idx = 0
	}
	if idx > n-1 {
		idx = n - 1
	}
	return idx
}

// uptimeFromRollups fills the summary from daily rollups. Latency percentiles are
// ok_count-weighted means of the daily percentiles (an exact percentile cannot be
// reconstructed from per-day percentiles — a documented approximation).
func (m *Module) uptimeFromRollups(ctx context.Context, siteID string, from, to, now time.Time, n int, sum *UptimeSummary) error {
	// `from`/`to` are the day-aligned bucket grid (both UTC midnight); `now` is the
	// real clock, used to locate the current day's not-yet-rolled-up checks.
	//
	// check_rollup only ever holds complete past UTC days — the nightly job runs
	// at 00:15 for the previous day, so the current day is never present. Read
	// rollups up to yesterday and fold today's raw checks in as a synthetic rollup
	// row; otherwise a same-day incident is invisible even though To claims
	// coverage up to now. (The raw scan is bounded to one day: ≤ ~288 rows.)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	lastRollupDay := timeutil.Day(todayStart.AddDate(0, 0, -1))
	rows, err := m.store.rollupsInRange(ctx, siteID, timeutil.Day(from), lastRollupDay)
	if err != nil {
		return err
	}
	today, ok, err := m.todayRollup(ctx, siteID, timeutil.Day(todayStart), todayStart, now)
	if err != nil {
		return err
	}
	if ok {
		rows = append(rows, today)
	}
	bucketDur := to.Sub(from) / time.Duration(n)

	type acc struct {
		ok, fail   int
		p50v, p50w []int
	}
	accs := make([]acc, n)
	var totOk, totFail int
	var oP50v, oP50w, oP95v, oP95w []int

	for _, r := range rows {
		dayT, err := time.Parse("2006-01-02", r.day)
		if err != nil {
			return err
		}
		i := bucketIndex(dayT.UTC(), from, bucketDur, n)
		accs[i].ok += r.okCount
		accs[i].fail += r.failCount
		if r.p50.Valid {
			accs[i].p50v = append(accs[i].p50v, int(r.p50.Int64))
			accs[i].p50w = append(accs[i].p50w, r.okCount)
			oP50v = append(oP50v, int(r.p50.Int64))
			oP50w = append(oP50w, r.okCount)
		}
		if r.p95.Valid {
			oP95v = append(oP95v, int(r.p95.Int64))
			oP95w = append(oP95w, r.okCount)
		}
		totOk += r.okCount
		totFail += r.failCount
	}

	for i := range sum.Buckets {
		a := accs[i]
		checks := a.ok + a.fail
		sum.Buckets[i].Checks = checks
		sum.Buckets[i].Failed = a.fail
		if checks > 0 {
			pct := 100 * float64(a.ok) / float64(checks)
			sum.Buckets[i].OkPct = &pct
		}
		if v, ok := weightedMean(a.p50v, a.p50w); ok {
			sum.Buckets[i].LatencyP50Ms = &v
		}
	}
	sum.ChecksTotal = totOk + totFail
	sum.ChecksFailed = totFail
	if sum.ChecksTotal > 0 {
		up := 100 * float64(totOk) / float64(sum.ChecksTotal)
		sum.UptimePct = &up
	}
	if v, ok := weightedMean(oP50v, oP50w); ok {
		sum.LatencyP50Ms = &v
	}
	if v, ok := weightedMean(oP95v, oP95w); ok {
		sum.LatencyP95Ms = &v
	}
	return nil
}

// todayRollup aggregates the current UTC day's raw checks into a synthetic rollup
// row (placed at day for bucketing), so a windowed summary reflects checks that
// the nightly rollup has not yet written to check_rollup. ok is false when the
// day has no raw checks. Its p50/p95 mirror how RollupDay derives them, keeping
// the ok-weighted-mean latency approximation consistent with real rollup rows.
func (m *Module) todayRollup(ctx context.Context, siteID, day string, from, to time.Time) (rollupRow, bool, error) {
	checks, err := m.store.rawChecksInRange(ctx, siteID, timeutil.Format(from), timeutil.Format(to))
	if err != nil {
		return rollupRow{}, false, err
	}
	if len(checks) == 0 {
		return rollupRow{}, false, nil
	}
	r := rollupRow{day: day}
	var lat []int
	for _, c := range checks {
		if c.ok {
			r.okCount++
			if c.latency.Valid {
				lat = append(lat, int(c.latency.Int64))
			}
		} else {
			r.failCount++
		}
	}
	if v, ok := percentile(lat, 50); ok {
		r.p50 = sql.NullInt64{Int64: int64(v), Valid: true}
	}
	if v, ok := percentile(lat, 95); ok {
		r.p95 = sql.NullInt64{Int64: int64(v), Valid: true}
	}
	return r, true, nil
}

// uptimeFromRaw fills the summary from raw checks (24h window only) with exact
// per-bucket and overall percentiles.
func (m *Module) uptimeFromRaw(ctx context.Context, siteID string, from, to time.Time, n int, sum *UptimeSummary) error {
	checks, err := m.store.rawChecksInRange(ctx, siteID, timeutil.Format(from), timeutil.Format(to))
	if err != nil {
		return err
	}
	bucketDur := to.Sub(from) / time.Duration(n)

	type acc struct {
		checks, failed int
		lat            []int
	}
	accs := make([]acc, n)
	var totChecks, totFailed int
	var allLat []int

	for _, c := range checks {
		i := bucketIndex(c.at, from, bucketDur, n)
		accs[i].checks++
		totChecks++
		if !c.ok {
			accs[i].failed++
			totFailed++
		}
		if c.ok && c.latency.Valid {
			accs[i].lat = append(accs[i].lat, int(c.latency.Int64))
			allLat = append(allLat, int(c.latency.Int64))
		}
	}

	for i := range sum.Buckets {
		a := accs[i]
		sum.Buckets[i].Checks = a.checks
		sum.Buckets[i].Failed = a.failed
		if a.checks > 0 {
			pct := 100 * float64(a.checks-a.failed) / float64(a.checks)
			sum.Buckets[i].OkPct = &pct
		}
		if v, ok := percentile(a.lat, 50); ok {
			sum.Buckets[i].LatencyP50Ms = &v
		}
	}
	sum.ChecksTotal = totChecks
	sum.ChecksFailed = totFailed
	if totChecks > 0 {
		up := 100 * float64(totChecks-totFailed) / float64(totChecks)
		sum.UptimePct = &up
	}
	if v, ok := percentile(allLat, 50); ok {
		sum.LatencyP50Ms = &v
	}
	if v, ok := percentile(allLat, 95); ok {
		sum.LatencyP95Ms = &v
	}
	return nil
}
