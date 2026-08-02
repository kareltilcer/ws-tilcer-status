package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/paging"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// Store is the monitoring persistence layer (checks, rollups, uptime reads).
type Store struct{ db *sql.DB }

// NewStore returns a store over db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// MonitoredSites returns the poll targets: sites with monitoring enabled and a
// URL set.
func (s *Store) MonitoredSites(ctx context.Context) ([]monitoredSite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, monitor_url, expected_status FROM site
		  WHERE monitor_enabled = 1 AND monitor_url IS NOT NULL AND monitor_url <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []monitoredSite
	for rows.Next() {
		var m monitoredSite
		if err := rows.Scan(&m.ID, &m.URL, &m.ExpectedStatus); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// WriteCheck records one check within tx and updates the site's cached
// reachability: last_checked_at, last_ok, and fail_streak (reset to 0 on
// success, incremented on failure — the red debounce counter).
func (s *Store) WriteCheck(ctx context.Context, tx *sql.Tx, o checkOutcome, checkedAt string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO check_result (site_id, checked_at, ok, status_code, latency_ms, error)
		 VALUES (?,?,?,?,?,?)`,
		o.SiteID, checkedAt, boolToInt(o.OK), niInt(o.StatusCode), niInt(o.LatencyMs), niStr(o.Err)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE site
		    SET last_checked_at = ?,
		        last_ok = ?,
		        fail_streak = CASE WHEN ? THEN 0 ELSE fail_streak + 1 END
		  WHERE id = ?`,
		checkedAt, boolToInt(o.OK), boolToInt(o.OK), o.SiteID)
	return err
}

// ListChecks returns a page of a site's check history, newest first.
func (s *Store) ListChecks(ctx context.Context, siteID string, limit int, cursor string) (CheckPage, error) {
	items, next, err := paging.Paginate(ctx, s.db, limit, cursor, paging.Query[CheckResult]{
		Columns:   "id, site_id, checked_at, ok, status_code, latency_ms, error",
		From:      "check_result",
		Filters:   []string{"site_id = ?"},
		Args:      []any{siteID},
		CursorCol: "checked_at",
		IDCol:     "id",
		Scan:      scanCheck,
		Key:       func(c CheckResult) (string, int64) { return c.CheckedAt, c.ID },
	})
	if err != nil {
		if errors.Is(err, paging.ErrInvalidCursor) {
			return CheckPage{}, errInvalidCursor
		}
		return CheckPage{}, err
	}
	return CheckPage{Items: items, NextCursor: next}, nil
}

func scanCheck(rows *sql.Rows) (CheckResult, error) {
	var (
		c       CheckResult
		okInt   int
		code    sql.NullInt64
		latency sql.NullInt64
		errStr  sql.NullString
	)
	if err := rows.Scan(&c.ID, &c.SiteID, &c.CheckedAt, &okInt, &code, &latency, &errStr); err != nil {
		return CheckResult{}, err
	}
	c.OK = okInt != 0
	c.StatusCode = nullIntPtr(code)
	c.LatencyMs = nullIntPtr(latency)
	c.Error = nullStrPtr(errStr)
	return c, nil
}

// SiteMeta returns whether a site exists and whether monitoring is enabled.
func (s *Store) SiteMeta(ctx context.Context, id string) (enabled, found bool, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT monitor_enabled FROM site WHERE id = ?", id).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return enabled, true, nil
}

// --- rollups ---

type dayAgg struct {
	okCount   int
	failCount int
	latencies []int // ok-sample latencies for percentile computation
}

// RollupDay aggregates one UTC day's raw checks into check_rollup for every site
// with checks that day (idempotent upsert). p50/p95 are computed in Go from that
// day's ok-sample latencies.
func (s *Store) RollupDay(ctx context.Context, day string) error {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return err
	}
	start := timeutil.Format(t)
	end := timeutil.Format(t.AddDate(0, 0, 1))

	return appdb.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT site_id, ok, latency_ms FROM check_result WHERE checked_at >= ? AND checked_at < ?`,
			start, end)
		if err != nil {
			return err
		}
		agg := map[string]*dayAgg{}
		for rows.Next() {
			var (
				siteID  string
				okInt   int
				latency sql.NullInt64
			)
			if err := rows.Scan(&siteID, &okInt, &latency); err != nil {
				_ = rows.Close()
				return err
			}
			a := agg[siteID]
			if a == nil {
				a = &dayAgg{}
				agg[siteID] = a
			}
			if okInt != 0 {
				a.okCount++
				if latency.Valid {
					a.latencies = append(a.latencies, int(latency.Int64))
				}
			} else {
				a.failCount++
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}

		for siteID, a := range agg {
			p50, ok50 := percentile(a.latencies, 50)
			p95, ok95 := percentile(a.latencies, 95)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO check_rollup (site_id, day, ok_count, fail_count, latency_p50_ms, latency_p95_ms)
				 VALUES (?,?,?,?,?,?)
				 ON CONFLICT(site_id, day) DO UPDATE SET
				   ok_count = excluded.ok_count,
				   fail_count = excluded.fail_count,
				   latency_p50_ms = excluded.latency_p50_ms,
				   latency_p95_ms = excluded.latency_p95_ms`,
				siteID, day, a.okCount, a.failCount, optInt(p50, ok50), optInt(p95, ok95)); err != nil {
				return err
			}
		}
		return nil
	})
}

// DaysNeedingRollup returns UTC days (<= upToDay) whose raw checks are not fully
// aggregated — the catch-up set so a downtime gap is aggregated before the purge
// can delete those raw rows. The test is per (site, day): a day is returned when,
// for any site, either no check_rollup row exists for (site_id, day) OR its
// ok_count+fail_count no longer equals the raw check count for that (site, day).
// The count check catches a late or backfilled check that lands on a (site, day)
// already rolled up — which a bare NOT EXISTS silently drops once any rollup row
// for that pair exists (clock skew across the 00:15 boundary, a manual re-roll).
// It also still re-flags a day rolled up for one site but not another. RollupDay
// re-aggregates every site for the returned day, so one site's mismatch correctly
// refreshes the whole day.
func (s *Store) DaysNeedingRollup(ctx context.Context, upToDay string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.day AS d FROM (
		          SELECT substr(cr.checked_at,1,10) AS day, cr.site_id AS site_id, COUNT(*) AS raw_count
		            FROM check_result cr
		           WHERE substr(cr.checked_at,1,10) <= ?
		           GROUP BY substr(cr.checked_at,1,10), cr.site_id
		        ) t
		   LEFT JOIN check_rollup ru ON ru.site_id = t.site_id AND ru.day = t.day
		   WHERE ru.site_id IS NULL OR ru.ok_count + ru.fail_count <> t.raw_count
		   GROUP BY t.day
		   ORDER BY t.day`, upToDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var days []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

// RefreshUptimeCache recomputes site.uptime_pct over [cutoffDay, today] from
// rollups. It is null for monitoring-disabled sites and for monitored sites with
// no checks in the window.
func (s *Store) RefreshUptimeCache(ctx context.Context, cutoffDay string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE site SET uptime_pct = (
		     SELECT CASE WHEN COALESCE(SUM(ok_count + fail_count), 0) = 0 THEN NULL
		                 ELSE 100.0 * SUM(ok_count) / SUM(ok_count + fail_count) END
		       FROM check_rollup WHERE site_id = site.id AND day >= ?
		   ) WHERE monitor_enabled = 1`, cutoffDay); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE site SET uptime_pct = NULL WHERE monitor_enabled = 0`)
	return err
}

// --- uptime endpoint reads ---

type rollupRow struct {
	day       string
	okCount   int
	failCount int
	p50       sql.NullInt64
	p95       sql.NullInt64
}

func (s *Store) rollupsInRange(ctx context.Context, siteID, fromDay, toDay string) ([]rollupRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT day, ok_count, fail_count, latency_p50_ms, latency_p95_ms
		   FROM check_rollup WHERE site_id = ? AND day >= ? AND day <= ? ORDER BY day`,
		siteID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rollupRow
	for rows.Next() {
		var r rollupRow
		if err := rows.Scan(&r.day, &r.okCount, &r.failCount, &r.p50, &r.p95); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type rawCheck struct {
	at      time.Time
	ok      bool
	latency sql.NullInt64
}

func (s *Store) rawChecksInRange(ctx context.Context, siteID, fromTS, toTS string) ([]rawCheck, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT checked_at, ok, latency_ms FROM check_result
		  WHERE site_id = ? AND checked_at >= ? AND checked_at < ? ORDER BY checked_at`,
		siteID, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawCheck
	for rows.Next() {
		var (
			at    string
			okInt int
			c     rawCheck
		)
		if err := rows.Scan(&at, &okInt, &c.latency); err != nil {
			return nil, err
		}
		t, err := timeutil.Parse(at)
		if err != nil {
			return nil, err
		}
		c.at = t
		c.ok = okInt != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- scan/value helpers ---

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func niInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func niStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func optInt(v int, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

func nullIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}
