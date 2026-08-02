package monitoring

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

func newMon(t *testing.T) (*Module, *sites.Store, *sql.DB) {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "mon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migFS, err := registry.MergeMigrations([]registry.MigrationSource{{Name: "sites", FS: sites.MigrationsFS}})
	if err != nil {
		t.Fatal(err)
	}
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mod := NewModule(db, Config{CheckTimeout: 2 * time.Second, PollConcurrency: 2, RedFailThreshold: 2, UptimeWindowDays: 90}, logger)
	return mod, sites.NewStore(db, 2), db
}

func createSite(t *testing.T, db *sql.DB, id, url string) {
	t.Helper()
	enabled := 0
	var u any
	if url != "" {
		enabled = 1
		u = url
	}
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO site (id, name, monitor_url, monitor_enabled, expected_status, crash_window_hours,
		    ingest_key_hash, cached_color, fail_streak, created_at)
		 VALUES (?,?,?,?,?,?,?, 'unknown', 0, ?)`,
		id, id, u, enabled, 200, 24, "hash", timeutil.Format(time.Now().UTC()))
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func TestPollerDebounceAndRecovery(t *testing.T) {
	mod, sst, db := newMon(t)
	ctx := context.Background()

	var status atomic.Int32
	status.Store(200)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer ts.Close()

	createSite(t, db, "fin", ts.URL)

	color := func() sites.Color {
		sum, err := sst.Get(ctx, "fin", time.Now().UTC())
		if err != nil || sum == nil {
			t.Fatalf("get: %v", err)
		}
		return sum.Color
	}

	// First OK check → green.
	mod.Poller().RunOnce(ctx)
	if c := color(); c != sites.Green {
		t.Fatalf("after first ok check, want green, got %q", c)
	}

	// One failure → held green (debounce, streak 1 < 2).
	status.Store(500)
	mod.Poller().RunOnce(ctx)
	if c := color(); c != sites.Green {
		t.Fatalf("single failure should hold green, got %q", c)
	}

	// Second consecutive failure → red.
	mod.Poller().RunOnce(ctx)
	if c := color(); c != sites.Red {
		t.Fatalf("two failures should be red, got %q", c)
	}

	// Recovery on next success → green.
	status.Store(200)
	mod.Poller().RunOnce(ctx)
	if c := color(); c != sites.Green {
		t.Fatalf("recovery should be green, got %q", c)
	}
}

func TestRollupAndUptime(t *testing.T) {
	mod, sst, db := newMon(t)
	ctx := context.Background()
	_ = sst
	createSite(t, db, "fin", "https://fin.tilcer.cz/readyz")

	// Insert a fixed day's raw checks: 4 ok (latencies 10,20,30,40) + 1 fail.
	day := "2026-07-01"
	base, _ := time.Parse("2006-01-02", day)
	insert := func(ok bool, latency int, offsetMin int) {
		at := timeutil.Format(base.Add(time.Duration(offsetMin) * time.Minute))
		var lat any = latency
		if !ok {
			lat = nil
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO check_result (site_id, checked_at, ok, status_code, latency_ms, error) VALUES ('fin',?,?,?,?,?)`,
			at, boolToInt(ok), 200, lat, nil); err != nil {
			t.Fatal(err)
		}
	}
	insert(true, 10, 1)
	insert(true, 20, 2)
	insert(true, 30, 3)
	insert(true, 40, 4)
	insert(false, 0, 5)

	if err := mod.Rollup().RunForDay(ctx, day); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	var okc, failc int
	var p50, p95 sql.NullInt64
	if err := db.QueryRowContext(ctx,
		`SELECT ok_count, fail_count, latency_p50_ms, latency_p95_ms FROM check_rollup WHERE site_id='fin' AND day=?`, day).
		Scan(&okc, &failc, &p50, &p95); err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if okc != 4 || failc != 1 {
		t.Fatalf("rollup counts: ok=%d fail=%d (want 4/1)", okc, failc)
	}
	if !p50.Valid || p50.Int64 != 20 || !p95.Valid || p95.Int64 != 40 {
		t.Fatalf("rollup percentiles: p50=%v p95=%v (want 20/40)", p50, p95)
	}

	// Uptime over 90d with now the following day: one bucket has data, rest are gaps.
	now, _ := time.Parse("2006-01-02", "2026-07-02")
	sum, found, err := mod.Uptime(ctx, "fin", "90d", 90, now)
	if err != nil || !found {
		t.Fatalf("uptime: %v found=%v", err, found)
	}
	if sum.UptimePct == nil || *sum.UptimePct < 79.9 || *sum.UptimePct > 80.1 {
		t.Fatalf("uptime_pct: %v (want ~80)", sum.UptimePct)
	}
	if sum.ChecksTotal != 5 || sum.ChecksFailed != 1 {
		t.Fatalf("checks: total=%d failed=%d (want 5/1)", sum.ChecksTotal, sum.ChecksFailed)
	}
	if len(sum.Buckets) != 90 {
		t.Fatalf("want 90 buckets, got %d", len(sum.Buckets))
	}
	withData, gaps := 0, 0
	for _, b := range sum.Buckets {
		if b.OkPct == nil {
			gaps++
		} else {
			withData++
		}
	}
	if withData != 1 {
		t.Fatalf("expected exactly 1 bucket with data, got %d", withData)
	}
	if gaps != 89 {
		t.Fatalf("expected 89 gap buckets (ok_pct null), got %d", gaps)
	}
}

// TestUptimeWindowIncludesToday verifies that a windowed (7d/30d/90d) summary
// reflects the current UTC day's raw checks even though the nightly rollup has
// not written them to check_rollup yet — otherwise a same-day incident is
// invisible while the To field still claims coverage up to now.
func TestUptimeWindowIncludesToday(t *testing.T) {
	mod, _, db := newMon(t)
	ctx := context.Background()
	createSite(t, db, "fin", "https://fin.tilcer.cz/readyz")

	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	base, _ := time.Parse("2006-01-02", timeutil.Day(now))
	insert := func(ok bool, offsetMin int) {
		at := timeutil.Format(base.Add(time.Duration(offsetMin) * time.Minute))
		if _, err := db.ExecContext(ctx,
			`INSERT INTO check_result (site_id, checked_at, ok, status_code, latency_ms, error) VALUES ('fin',?,?,200,NULL,NULL)`,
			at, boolToInt(ok)); err != nil {
			t.Fatal(err)
		}
	}
	// 3 ok, 1 fail today — deliberately never rolled up.
	insert(true, 1)
	insert(true, 2)
	insert(true, 3)
	insert(false, 4)

	sum, found, err := mod.Uptime(ctx, "fin", "90d", 90, now)
	if err != nil || !found {
		t.Fatalf("uptime: %v found=%v", err, found)
	}
	if sum.ChecksTotal != 4 || sum.ChecksFailed != 1 {
		t.Fatalf("today's raw checks not reflected: total=%d failed=%d (want 4/1)", sum.ChecksTotal, sum.ChecksFailed)
	}
	if sum.UptimePct == nil || *sum.UptimePct < 74.9 || *sum.UptimePct > 75.1 {
		t.Fatalf("uptime_pct: %v (want ~75)", sum.UptimePct)
	}
	// The incident lands in the last (current-day) bucket.
	last := sum.Buckets[len(sum.Buckets)-1]
	if last.Checks != 4 || last.Failed != 1 {
		t.Fatalf("last bucket: checks=%d failed=%d (want 4/1)", last.Checks, last.Failed)
	}
}

// TestUptimeBucketsAreDayAligned verifies that when the request arrives at a
// non-midnight time, a calendar day's rollup lands in the bucket LABELLED with
// that same day (start date), not a neighbouring one. Before the day-aligned grid
// the bucket boundaries were time-of-day-aligned (to := now), so a midnight-keyed
// rollup fell into the bucket labelled the previous day — the tooltip showed the
// wrong day's data.
func TestUptimeBucketsAreDayAligned(t *testing.T) {
	mod, _, db := newMon(t)
	ctx := context.Background()
	createSite(t, db, "fin", "https://fin.tilcer.cz/readyz")

	// A completed rollup for one specific past UTC day: 4 ok, 1 fail.
	const day = "2026-07-20"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO check_rollup (site_id, day, ok_count, fail_count, latency_p50_ms, latency_p95_ms)
		 VALUES ('fin', ?, 4, 1, 20, 40)`, day); err != nil {
		t.Fatal(err)
	}

	// Request at a deliberately non-midnight time, five days later.
	now := time.Date(2026, 7, 25, 14, 37, 0, 0, time.UTC)
	sum, found, err := mod.Uptime(ctx, "fin", "90d", 90, now)
	if err != nil || !found {
		t.Fatalf("uptime: %v found=%v", err, found)
	}

	withData := 0
	var dataBucket UptimeBucket
	for _, b := range sum.Buckets {
		if b.OkPct != nil {
			withData++
			dataBucket = b
		}
	}
	if withData != 1 {
		t.Fatalf("expected exactly 1 bucket with data, got %d", withData)
	}
	if got := dataBucket.Start[:10]; got != day {
		t.Fatalf("data landed in bucket labelled %q, want %q (bucket/day misaligned)", got, day)
	}
	if dataBucket.Checks != 5 || dataBucket.Failed != 1 {
		t.Fatalf("data bucket: checks=%d failed=%d (want 5/1)", dataBucket.Checks, dataBucket.Failed)
	}
	if sum.UptimePct == nil || *sum.UptimePct < 79.9 || *sum.UptimePct > 80.1 {
		t.Fatalf("uptime_pct: %v (want ~80)", sum.UptimePct)
	}
}

// TestDaysNeedingRollupIsPerSite verifies a late check for one site on a day
// already rolled up for another site still re-flags that day: the "not yet
// aggregated" test is per (site, day), not a global NOT IN over check_rollup.day.
func TestDaysNeedingRollupIsPerSite(t *testing.T) {
	mod, _, db := newMon(t)
	ctx := context.Background()
	createSite(t, db, "a", "https://a.tilcer.cz/readyz")
	createSite(t, db, "b", "https://b.tilcer.cz/readyz")

	day := "2026-07-01"
	base, _ := time.Parse("2006-01-02", day)
	at := timeutil.Format(base.Add(time.Minute))
	check := func(site string) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO check_result (site_id, checked_at, ok, status_code, latency_ms, error) VALUES (?,?,1,200,10,NULL)`,
			site, at); err != nil {
			t.Fatal(err)
		}
	}

	// Site a is checked and rolled up; the day now exists in check_rollup.
	check("a")
	if err := mod.Rollup().RunForDay(ctx, day); err != nil {
		t.Fatal(err)
	}

	// A late check for site b lands on that same, already-rolled day.
	check("b")

	days, err := mod.store.DaysNeedingRollup(ctx, "2026-07-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0] != day {
		t.Fatalf("late per-site check should re-flag %s, got %v", day, days)
	}

	// Re-rolling aggregates site b and settles the day.
	if err := mod.Rollup().RunForDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	days, err = mod.store.DaysNeedingRollup(ctx, "2026-07-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 0 {
		t.Fatalf("day should be settled after re-roll, got %v", days)
	}
	var bOk int
	if err := db.QueryRowContext(ctx, `SELECT ok_count FROM check_rollup WHERE site_id='b' AND day=?`, day).Scan(&bOk); err != nil {
		t.Fatalf("site b rollup missing after re-roll: %v", err)
	}
	if bOk != 1 {
		t.Fatalf("site b ok_count=%d (want 1)", bOk)
	}
}

// TestDaysNeedingRollupReflagsLateSameSiteCheck verifies a late check that lands
// on a (site, day) already rolled up for that same site re-flags the day, so it
// is re-aggregated instead of being silently dropped and then purged.
func TestDaysNeedingRollupReflagsLateSameSiteCheck(t *testing.T) {
	mod, _, db := newMon(t)
	ctx := context.Background()
	createSite(t, db, "a", "https://a.tilcer.cz/readyz")

	day := "2026-07-01"
	base, _ := time.Parse("2006-01-02", day)
	check := func(offset time.Duration) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO check_result (site_id, checked_at, ok, status_code, latency_ms, error) VALUES ('a',?,1,200,10,NULL)`,
			timeutil.Format(base.Add(offset))); err != nil {
			t.Fatal(err)
		}
	}

	// One check, rolled up: the (a, day) pair now exists and matches.
	check(time.Minute)
	if err := mod.Rollup().RunForDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	days, err := mod.store.DaysNeedingRollup(ctx, "2026-07-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 0 {
		t.Fatalf("settled day should not be flagged, got %v", days)
	}

	// A late check for the SAME site on the SAME, already-rolled day.
	check(2 * time.Minute)
	days, err = mod.store.DaysNeedingRollup(ctx, "2026-07-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0] != day {
		t.Fatalf("late same-site check should re-flag %s, got %v", day, days)
	}

	// Re-rolling folds the late check in and settles the day.
	if err := mod.Rollup().RunForDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	days, err = mod.store.DaysNeedingRollup(ctx, "2026-07-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 0 {
		t.Fatalf("day should be settled after re-roll, got %v", days)
	}
	var okCount int
	if err := db.QueryRowContext(ctx, `SELECT ok_count FROM check_rollup WHERE site_id='a' AND day=?`, day).Scan(&okCount); err != nil {
		t.Fatal(err)
	}
	if okCount != 2 {
		t.Fatalf("site a ok_count=%d (want 2 after late check folded in)", okCount)
	}
}

func TestUptimeMonitoringOff(t *testing.T) {
	mod, sst, db := newMon(t)
	ctx := context.Background()
	_ = sst
	createSite(t, db, "yarnlog", "") // crash-only

	sum, found, err := mod.Uptime(ctx, "yarnlog", "90d", 90, time.Now().UTC())
	if err != nil || !found {
		t.Fatalf("uptime: %v found=%v", err, found)
	}
	if sum.UptimePct != nil {
		t.Fatalf("monitoring-off site should have null uptime_pct, got %v", *sum.UptimePct)
	}
	for _, b := range sum.Buckets {
		if b.OkPct != nil {
			t.Fatalf("monitoring-off buckets should all be gaps")
		}
	}
}
