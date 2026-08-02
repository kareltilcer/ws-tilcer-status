package sites

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// ErrDuplicate is returned by Create when the site id already exists (→ 409).
var ErrDuplicate = errors.New("sites: duplicate id")

// ErrMonitorURLRequired is returned by Update when a PATCH asks to enable
// monitoring but no monitor_url is (or would be) set (→ 422).
var ErrMonitorURLRequired = errors.New("sites: monitor_url required to enable monitoring")

// Store is the sites registry persistence layer.
type Store struct {
	db           *sql.DB
	redThreshold int
}

// NewStore returns a store over db. redThreshold is the consecutive-failure count
// at which a monitored site goes red (FR-6), used to compute color on read.
func NewStore(db *sql.DB, redThreshold int) *Store {
	return &Store{db: db, redThreshold: redThreshold}
}

const siteCols = `s.id, s.name, s.monitor_url, s.monitor_enabled, s.expected_status, s.crash_window_hours,
	s.cached_color, s.last_checked_at, s.last_ok, s.fail_streak, s.uptime_pct, s.created_at,
	(SELECT COUNT(*) FROM crash_group g WHERE g.site_id = s.id AND g.status = 'open') AS open_groups`

type rawSite struct {
	id               string
	name             string
	monitorURL       sql.NullString
	monitorEnabled   bool
	expectedStatus   int
	crashWindowHours int
	cachedColor      string
	lastCheckedAt    sql.NullString
	lastOK           sql.NullInt64
	failStreak       int
	uptimePct        sql.NullFloat64
	createdAt        string
	openGroups       int
}

func scanSite(row interface{ Scan(...any) error }) (rawSite, error) {
	var r rawSite
	err := row.Scan(&r.id, &r.name, &r.monitorURL, &r.monitorEnabled, &r.expectedStatus, &r.crashWindowHours,
		&r.cachedColor, &r.lastCheckedAt, &r.lastOK, &r.failStreak, &r.uptimePct, &r.createdAt, &r.openGroups)
	return r, err
}

// toSummary builds a wire summary from a raw row, recomputing color on read
// (orange ages out purely by time, so the cached value can be stale). It runs a
// per-site crash-count query; List uses summaryFrom with a batched count instead.
func (s *Store) toSummary(ctx context.Context, r rawSite, now time.Time) (SiteSummary, error) {
	recent, err := countRecentOpenCrashes(ctx, s.db, r.id, r.crashWindowHours, now)
	if err != nil {
		return SiteSummary{}, err
	}
	return s.summaryFrom(r, recent, now), nil
}

// summaryFrom assembles a wire summary from a raw row and an already-computed
// recent-open-crash count (pure — no query), recomputing color on read.
func (s *Store) summaryFrom(r rawSite, recent int, now time.Time) SiteSummary {
	color := ComputeColor(ColorInputs{
		MonitorEnabled:    r.monitorEnabled,
		FailStreak:        r.failStreak,
		RedThreshold:      s.redThreshold,
		RecentOpenCrashes: recent,
		HasAnyCheck:       r.lastCheckedAt.Valid && r.lastCheckedAt.String != "",
		PriorColor:        Color(r.cachedColor),
	})
	return SiteSummary{
		ID:               r.id,
		Name:             r.name,
		Color:            color,
		MonitorURL:       nsToPtr(r.monitorURL),
		MonitorEnabled:   r.monitorEnabled,
		ExpectedStatus:   r.expectedStatus,
		CrashWindowHours: r.crashWindowHours,
		LastCheckedAt:    nsToPtr(r.lastCheckedAt),
		LastOK:           nullBoolPtr(r.lastOK),
		FailStreak:       r.failStreak,
		UptimePct:        nullFloatPtr(r.uptimePct),
		OpenCrashGroups:  r.openGroups,
		RecentCrashCount: recent,
		CreatedAt:        r.createdAt,
	}
}

// List returns every site with its computed color and headline counts, oldest
// first (registration order).
//
// The raw rows are fully drained and the cursor closed BEFORE any further query:
// the pool is capped at a single connection (SetMaxOpenConns(1)), so querying
// while the outer rows cursor is still open would deadlock waiting for the
// connection it holds. The recent-open-crash counts for every site are then
// fetched in ONE batched query (not a per-site round-trip) and folded in.
func (s *Store) List(ctx context.Context, now time.Time) ([]SiteSummary, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+siteCols+" FROM site s ORDER BY s.created_at ASC, s.id ASC")
	if err != nil {
		return nil, err
	}
	var raws []rawSite
	for rows.Next() {
		r, err := scanSite(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		raws = append(raws, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	counts, err := s.recentOpenCrashCounts(ctx, raws, now)
	if err != nil {
		return nil, err
	}

	out := make([]SiteSummary, 0, len(raws))
	for _, r := range raws {
		out = append(out, s.summaryFrom(r, counts[r.id], now))
	}
	return out, nil
}

// recentOpenCrashCounts returns, per site id, the number of crash events in OPEN
// groups within that site's own crash_window_hours — the same predicate as
// countRecentOpenCrashes, but for every site in ONE query instead of a per-site
// round-trip (which serializes badly on the single writer connection). A single
// SQL lower bound of now-max(window) bounds the scan; each site's exact window is
// then applied in Go, so the count matches the per-site query precisely.
func (s *Store) recentOpenCrashCounts(ctx context.Context, raws []rawSite, now time.Time) (map[string]int, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	windowStart := make(map[string]string, len(raws))
	maxHours := 0
	for _, r := range raws {
		windowStart[r.id] = timeutil.Format(now.Add(-time.Duration(r.crashWindowHours) * time.Hour))
		if r.crashWindowHours > maxHours {
			maxHours = r.crashWindowHours
		}
	}
	lower := timeutil.Format(now.Add(-time.Duration(maxHours) * time.Hour))
	upper := timeutil.Format(now)

	rows, err := s.db.QueryContext(ctx,
		`SELECT g.site_id, e.occurred_at FROM crash_event e
		   JOIN crash_group g ON g.id = e.group_id
		  WHERE g.status = 'open' AND e.occurred_at > ? AND e.occurred_at <= ?`,
		lower, upper)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int, len(raws))
	for rows.Next() {
		var siteID, occurredAt string
		if err := rows.Scan(&siteID, &occurredAt); err != nil {
			return nil, err
		}
		// Apply the site's exact window (the SQL lower bound is the widest window).
		if ws, ok := windowStart[siteID]; ok && occurredAt > ws {
			counts[siteID]++
		}
	}
	return counts, rows.Err()
}

// Get returns one site summary, or (nil, nil) when it does not exist.
func (s *Store) Get(ctx context.Context, id string, now time.Time) (*SiteSummary, error) {
	r, err := scanSite(s.db.QueryRowContext(ctx, "SELECT "+siteCols+" FROM site s WHERE s.id = ?", id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sum, err := s.toSummary(ctx, r, now)
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

// Exists reports whether a site id is taken.
func (s *Store) Exists(ctx context.Context, id string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM site WHERE id = ?", id).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Create inserts a new site (initial color unknown, no checks/crashes yet).
// Returns ErrDuplicate when the id is taken.
func (s *Store) Create(ctx context.Context, p createParams, now time.Time) (*SiteSummary, error) {
	exists, err := s.Exists(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrDuplicate
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO site
		   (id, name, monitor_url, monitor_enabled, expected_status, crash_window_hours,
		    ingest_key_hash, cached_color, fail_streak, created_at)
		 VALUES (?,?,?,?,?,?,?, 'unknown', 0, ?)`,
		p.ID, p.Name, nullIfEmpty(p.MonitorURL), boolToInt(p.MonitorEnabled), p.ExpectedStatus,
		p.CrashWindowHours, p.IngestKeyHash, timeutil.Format(now))
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, p.ID, now)
}

// Update applies a validated PATCH and returns the updated summary, or (nil, nil)
// when the site does not exist. The invariant monitor_enabled ⇒ monitor_url is
// enforced here: explicitly enabling monitoring without a URL is rejected
// (ErrMonitorURLRequired), while merely clearing the URL forces monitoring off.
// A changed monitor target (new URL, a new expected_status, or monitoring turned
// back on) also resets the cached reachability so a stale fail_streak can't color
// the site from the old criterion before the next poll runs.
func (s *Store) Update(ctx context.Context, id string, p updateParams, now time.Time) (*SiteSummary, error) {
	var (
		curName     string
		curURL      sql.NullString
		curEnabled  bool
		curExpected int
		curWindow   int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT name, monitor_url, monitor_enabled, expected_status, crash_window_hours FROM site WHERE id = ?`, id).
		Scan(&curName, &curURL, &curEnabled, &curExpected, &curWindow)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	name := curName
	if p.Name != nil {
		name = *p.Name
	}
	monURL := curURL.String
	if p.MonitorURLSet {
		monURL = p.MonitorURL
	}
	enabled := curEnabled
	if p.MonitorEnabledSet {
		enabled = p.MonitorEnabled
	}
	expected := curExpected
	if p.ExpectedStatus != nil {
		expected = *p.ExpectedStatus
	}
	window := curWindow
	if p.CrashWindowHours != nil {
		window = *p.CrashWindowHours
	}
	if monURL == "" {
		if p.MonitorEnabledSet && p.MonitorEnabled {
			// The caller explicitly asked to enable monitoring but there is no URL
			// to poll — surface it instead of silently saving monitoring off.
			return nil, ErrMonitorURLRequired
		}
		enabled = false // no URL to poll ⇒ monitoring off
	}

	// Reset the reachability cache when the monitor target or its pass/fail
	// criterion changes, or monitoring is turned back on: the leftover
	// fail_streak/last_ok were evaluated against the old URL/expected_status and
	// would otherwise color the site (e.g. red) until the next poll overwrites them.
	resetReachability := monURL != curURL.String || expected != curExpected || (enabled && !curEnabled)
	query := `UPDATE site SET name = ?, monitor_url = ?, monitor_enabled = ?, expected_status = ?, crash_window_hours = ?`
	args := []any{name, nullIfEmpty(monURL), boolToInt(enabled), expected, window}
	if resetReachability {
		query += `, fail_streak = 0, last_ok = NULL, last_checked_at = NULL, cached_color = 'unknown'`
	}
	query += ` WHERE id = ?`
	args = append(args, id)
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return nil, err
	}
	return s.Get(ctx, id, now)
}

// Delete removes a site (cascading its checks, rollups, groups, and events).
// Returns false when the id did not exist.
func (s *Store) Delete(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM site WHERE id = ?", id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetIngestKeyHash replaces a site's ingest key hash (rotate). Returns false when
// the id did not exist.
func (s *Store) SetIngestKeyHash(ctx context.Context, id, hash string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE site SET ingest_key_hash = ? WHERE id = ?", hash, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// IngestKeyHash returns a site's stored ingest key hash. found is false when the
// site does not exist (used by the public ingest path to distinguish 404 from 401).
func (s *Store) IngestKeyHash(ctx context.Context, id string) (hash string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT ingest_key_hash FROM site WHERE id = ?", id).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

// --- scan helpers ---

func nsToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func nullBoolPtr(n sql.NullInt64) *bool {
	if !n.Valid {
		return nil
	}
	b := n.Int64 != 0
	return &b
}

func nullFloatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
