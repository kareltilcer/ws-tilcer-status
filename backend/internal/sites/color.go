package sites

import (
	"context"
	"database/sql"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// Color is the per-site status (openapi Color).
type Color string

const (
	Red     Color = "red"
	Orange  Color = "orange"
	Green   Color = "green"
	Unknown Color = "unknown"
)

// ColorInputs are the pure inputs to the FR-6 status mapping.
type ColorInputs struct {
	MonitorEnabled    bool
	FailStreak        int
	RedThreshold      int
	RecentOpenCrashes int   // crash events in OPEN groups within crash_window_hours
	HasAnyCheck       bool  // last_checked_at IS NOT NULL
	PriorColor        Color // current cached_color — held during the sub-threshold debounce
}

// ComputeColor maps a site's signals to a color (PRD FR-6). Precedence
// red > orange > green > unknown is encoded by branch order. The 2-fail red
// debounce falls out: at a streak below the threshold with no crash signal the
// function returns PriorColor, so a single failed check holds the prior color
// rather than flipping the site red. A monitoring-disabled site is never red.
func ComputeColor(in ColorInputs) Color {
	if in.MonitorEnabled && in.FailStreak >= in.RedThreshold {
		return Red
	}
	if in.RecentOpenCrashes > 0 {
		return Orange
	}
	if !in.MonitorEnabled {
		// Crash-only site with no qualifying crashes: green means "no problems
		// reported", never "actively confirmed up". Never red, never unknown.
		return Green
	}
	if !in.HasAnyCheck {
		return Unknown
	}
	if in.FailStreak > 0 {
		// One (or more, but still sub-threshold) failed checks: hold the prior
		// color rather than flip to red until the streak reaches the threshold.
		// But never hold a stale orange: reaching here means RecentOpenCrashes is
		// 0 (the crash that colored it orange has aged out), so the pre-failure
		// reachability color was green.
		if in.PriorColor == Orange {
			return Green
		}
		return in.PriorColor
	}
	return Green
}

// siteColorState is the subset of the site row needed to recompute color.
type siteColorState struct {
	MonitorEnabled   bool
	FailStreak       int
	HasAnyCheck      bool
	CrashWindowHours int
	CachedColor      Color
}

// txQuerier is satisfied by *sql.Tx and *sql.DB.
type txQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RecomputeAndPersist recomputes a site's color from its current fail_streak,
// monitor_enabled, and recent open-group crash count, then writes it to
// cached_color. It runs inside the caller's transaction (poll check write, crash
// ingest, or triage) so the color update is atomic with its trigger. Returns the
// new color. Under SetMaxOpenConns(1) + BEGIN IMMEDIATE all writes serialize, so
// no lock is needed.
func RecomputeAndPersist(ctx context.Context, q txQuerier, siteID string, redThreshold int, now time.Time) (Color, error) {
	st, err := loadColorState(ctx, q, siteID)
	if err != nil {
		return "", err
	}
	recent, err := countRecentOpenCrashes(ctx, q, siteID, st.CrashWindowHours, now)
	if err != nil {
		return "", err
	}
	color := ComputeColor(ColorInputs{
		MonitorEnabled:    st.MonitorEnabled,
		FailStreak:        st.FailStreak,
		RedThreshold:      redThreshold,
		RecentOpenCrashes: recent,
		HasAnyCheck:       st.HasAnyCheck,
		PriorColor:        st.CachedColor,
	})
	if _, err := q.ExecContext(ctx, `UPDATE site SET cached_color = ? WHERE id = ?`, string(color), siteID); err != nil {
		return "", err
	}
	return color, nil
}

func loadColorState(ctx context.Context, q txQuerier, siteID string) (siteColorState, error) {
	var (
		st          siteColorState
		lastChecked sql.NullString
		cached      string
	)
	err := q.QueryRowContext(ctx,
		`SELECT monitor_enabled, fail_streak, last_checked_at, crash_window_hours, cached_color
		   FROM site WHERE id = ?`, siteID).
		Scan(&st.MonitorEnabled, &st.FailStreak, &lastChecked, &st.CrashWindowHours, &cached)
	if err != nil {
		return siteColorState{}, err
	}
	st.HasAnyCheck = lastChecked.Valid && lastChecked.String != ""
	st.CachedColor = Color(cached)
	return st, nil
}

// countRecentOpenCrashes counts crash events in OPEN groups whose occurred_at
// falls in (now-crashWindowHours, now], for one site. The upper bound is
// defense-in-depth: ingest already clamps future timestamps, so any occurred_at
// ahead of now can only be pre-existing bad data, which must still age out.
func countRecentOpenCrashes(ctx context.Context, q txQuerier, siteID string, crashWindowHours int, now time.Time) (int, error) {
	windowStart := timeutil.Format(now.Add(-time.Duration(crashWindowHours) * time.Hour))
	windowEnd := timeutil.Format(now)
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM crash_event e
		   JOIN crash_group g ON g.id = e.group_id
		  WHERE e.site_id = ? AND g.status = 'open'
		    AND e.occurred_at > ? AND e.occurred_at <= ?`,
		siteID, windowStart, windowEnd).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
