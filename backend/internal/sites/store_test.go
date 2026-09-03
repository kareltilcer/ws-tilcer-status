package sites

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migFS, err := registry.MergeMigrations([]registry.MigrationSource{{Name: "sites", FS: MigrationsFS}})
	if err != nil {
		t.Fatalf("merge migrations: %v", err)
	}
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(db, 2)
}

func mustCreate(t *testing.T, s *Store, id, name, url string, enabled bool) *SiteSummary {
	t.Helper()
	_, hash, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sum, err := s.Create(context.Background(), createParams{
		ID: id, Name: name, MonitorURL: url, MonitorEnabled: enabled,
		ExpectedStatus: 200, CrashWindowHours: 24, IngestKeyHash: hash,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	return sum
}

func TestStoreCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sum := mustCreate(t, s, "fin", "Finance", "https://fin.tilcer.cz/readyz", true)
	if sum.Color != Unknown {
		t.Fatalf("new monitored site should be unknown, got %q", sum.Color)
	}
	if sum.UptimePct != nil {
		t.Fatalf("new site uptime_pct should be null")
	}

	got, err := s.Get(ctx, "fin", now)
	if err != nil || got == nil {
		t.Fatalf("get fin: %v (nil=%v)", err, got == nil)
	}
	if got.Name != "Finance" || !got.MonitorEnabled {
		t.Fatalf("unexpected site: %+v", got)
	}

	list, err := s.List(ctx, now)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	// Update the name and disable monitoring.
	newName := "Finance App"
	upd, err := s.Update(ctx, "fin", updateParams{Name: &newName, MonitorEnabledSet: true, MonitorEnabled: false}, now)
	if err != nil || upd == nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "Finance App" || upd.MonitorEnabled {
		t.Fatalf("update not applied: %+v", upd)
	}

	// Rotate key.
	_, hash2, _ := GenerateKey()
	ok, err := s.SetIngestKeyHash(ctx, "fin", hash2)
	if err != nil || !ok {
		t.Fatalf("rotate: %v ok=%v", err, ok)
	}
	stored, found, err := s.IngestKeyHash(ctx, "fin")
	if err != nil || !found || stored != hash2 {
		t.Fatalf("ingest key hash mismatch after rotate")
	}

	// Delete (no object collector registered — this deployment has no feedback).
	_, deleted, err := s.Delete(ctx, "fin", nil)
	if err != nil || !deleted {
		t.Fatalf("delete: %v ok=%v", err, deleted)
	}
	gone, _ := s.Get(ctx, "fin", now)
	if gone != nil {
		t.Fatalf("site should be gone after delete")
	}
}

func TestStoreDuplicate(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "home", "Home", "https://home.tilcer.cz/readyz", true)
	_, hash, _ := GenerateKey()
	_, err := s.Create(context.Background(), createParams{
		ID: "home", Name: "Home 2", ExpectedStatus: 200, CrashWindowHours: 24, IngestKeyHash: hash,
	}, time.Now().UTC())
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

// TestUpdateEnableWithoutURL verifies a PATCH that asks to enable monitoring on a
// site with no URL is rejected rather than silently saved as disabled.
func TestUpdateEnableWithoutURL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustCreate(t, s, "jidlo", "Jidlo", "", false) // crash-only, no URL

	_, err := s.Update(ctx, "jidlo", updateParams{MonitorEnabledSet: true, MonitorEnabled: true}, now)
	if !errors.Is(err, ErrMonitorURLRequired) {
		t.Fatalf("enabling monitoring without a URL should fail with ErrMonitorURLRequired, got %v", err)
	}
}

// TestUpdateResetsReachabilityOnReEnable verifies that re-enabling monitoring
// clears a stale fail_streak so the site is not colored red from the old target
// before the next poll runs.
func TestUpdateResetsReachabilityOnReEnable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustCreate(t, s, "fin", "Finance", "https://fin.tilcer.cz/readyz", true)

	// Simulate a red site: a failing streak with a stale cached color.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE site SET fail_streak = 5, cached_color = 'red', last_ok = 0, last_checked_at = ? WHERE id = 'fin'`,
		timeutil.Format(now)); err != nil {
		t.Fatal(err)
	}

	// Disabling alone must not wipe the streak.
	if _, err := s.Update(ctx, "fin", updateParams{MonitorEnabledSet: true, MonitorEnabled: false}, now); err != nil {
		t.Fatalf("disable: %v", err)
	}
	var streak int
	if err := s.db.QueryRowContext(ctx, "SELECT fail_streak FROM site WHERE id = 'fin'").Scan(&streak); err != nil {
		t.Fatal(err)
	}
	if streak != 5 {
		t.Fatalf("disabling should not reset fail_streak, got %d", streak)
	}

	// Re-enabling must reset the reachability cache.
	upd, err := s.Update(ctx, "fin", updateParams{MonitorEnabledSet: true, MonitorEnabled: true}, now)
	if err != nil || upd == nil {
		t.Fatalf("re-enable: %v", err)
	}
	if upd.FailStreak != 0 {
		t.Fatalf("re-enable should reset fail_streak, got %d", upd.FailStreak)
	}
	if upd.Color == Red {
		t.Fatalf("re-enabled site should not be red from stale data, got %q", upd.Color)
	}
}

// TestUpdateResetsReachabilityOnExpectedStatusChange verifies that changing the
// expected_status clears a stale fail_streak/color: the old streak was evaluated
// against the previous pass/fail criterion, so it must not carry over.
func TestUpdateResetsReachabilityOnExpectedStatusChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustCreate(t, s, "fin", "Finance", "https://fin.tilcer.cz/readyz", true)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE site SET fail_streak = 5, cached_color = 'red', last_ok = 0, last_checked_at = ? WHERE id = 'fin'`,
		timeutil.Format(now)); err != nil {
		t.Fatal(err)
	}

	newExpected := 204
	upd, err := s.Update(ctx, "fin", updateParams{ExpectedStatus: &newExpected}, now)
	if err != nil || upd == nil {
		t.Fatalf("update: %v", err)
	}
	if upd.ExpectedStatus != 204 {
		t.Fatalf("expected_status not applied: %d", upd.ExpectedStatus)
	}
	if upd.FailStreak != 0 {
		t.Fatalf("changing expected_status should reset fail_streak, got %d", upd.FailStreak)
	}
	if upd.Color == Red {
		t.Fatalf("site should not stay red on criterion change, got %q", upd.Color)
	}
}

// TestUpdateKeepsReachabilityWhenCriterionUnchanged guards against over-resetting:
// a PATCH that touches neither the URL, expected_status, nor the enabled flag must
// preserve the existing fail_streak.
func TestUpdateKeepsReachabilityWhenCriterionUnchanged(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustCreate(t, s, "fin", "Finance", "https://fin.tilcer.cz/readyz", true)
	if _, err := s.db.ExecContext(ctx, `UPDATE site SET fail_streak = 3 WHERE id = 'fin'`); err != nil {
		t.Fatal(err)
	}
	newName := "Finance 2"
	upd, err := s.Update(ctx, "fin", updateParams{Name: &newName}, now)
	if err != nil || upd == nil {
		t.Fatalf("update: %v", err)
	}
	if upd.FailStreak != 3 {
		t.Fatalf("a name-only PATCH must not reset fail_streak, got %d", upd.FailStreak)
	}
}

// TestListBatchedRecentCrashCounts verifies the single batched query List uses
// for recent-open-crash counts reproduces the per-site predicate exactly: each
// site's OWN crash_window_hours is applied (not the widest across the fleet),
// only OPEN groups count, and every site is counted in one round-trip.
func TestListBatchedRecentCrashCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// site "a" — default 24h crash window.
	mustCreate(t, s, "a", "A", "https://a.tilcer.cz/readyz", true)
	// site "b" — a narrow 2h crash window.
	_, hash, _ := GenerateKey()
	if _, err := s.Create(ctx, createParams{
		ID: "b", Name: "B", MonitorURL: "https://b.tilcer.cz/readyz", MonitorEnabled: true,
		ExpectedStatus: 200, CrashWindowHours: 2, IngestKeyHash: hash,
	}, now); err != nil {
		t.Fatalf("create b: %v", err)
	}

	// addEvent inserts a group in the given status with one event `ageHours` old.
	addEvent := func(site, status string, ageHours int) {
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO crash_group (site_id, fingerprint, title, level, count, status, first_seen, last_seen)
			 VALUES (?, ?, 'boom', 'error', 1, ?, ?, ?)`,
			site, "fp-"+site+status+strconv.Itoa(ageHours), status, timeutil.Format(now), timeutil.Format(now))
		if err != nil {
			t.Fatalf("insert group: %v", err)
		}
		gid, _ := res.LastInsertId()
		at := timeutil.Format(now.Add(-time.Duration(ageHours) * time.Hour))
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO crash_event (site_id, group_id, level, message, occurred_at, received_at)
			 VALUES (?, ?, 'error', 'boom', ?, ?)`, site, gid, at, at); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	addEvent("a", "open", 1)     // within a's 24h window → counts
	addEvent("a", "open", 30)    // older than 24h → excluded
	addEvent("a", "resolved", 1) // recent but resolved group → excluded
	addEvent("b", "open", 1)     // within b's 2h window → counts
	addEvent("b", "open", 3)     // within the 24h widest bound but outside b's 2h → excluded

	list, err := s.List(ctx, now)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]int{}
	colors := map[string]Color{}
	for _, sm := range list {
		got[sm.ID] = sm.RecentCrashCount
		colors[sm.ID] = sm.Color
	}
	if got["a"] != 1 {
		t.Fatalf("site a recent count = %d, want 1 (24h window, open groups only)", got["a"])
	}
	if got["b"] != 1 {
		t.Fatalf("site b recent count = %d, want 1 (2h window must exclude the 3h-old event)", got["b"])
	}
	// A recent open crash colors a monitored, as-yet-unchecked site orange.
	if colors["a"] != Orange || colors["b"] != Orange {
		t.Fatalf("sites with a recent open crash should be orange, got a=%q b=%q", colors["a"], colors["b"])
	}

	// The batched count must equal the per-site query it replaced, for every site.
	for _, id := range []string{"a", "b"} {
		var win int
		if err := s.db.QueryRowContext(ctx, "SELECT crash_window_hours FROM site WHERE id = ?", id).Scan(&win); err != nil {
			t.Fatal(err)
		}
		want, err := countRecentOpenCrashes(ctx, s.db, id, win, now)
		if err != nil {
			t.Fatal(err)
		}
		if got[id] != want {
			t.Fatalf("site %s: batched=%d per-site=%d mismatch", id, got[id], want)
		}
	}
}

// TestCascadeDelete verifies the ON DELETE CASCADE FKs (and the foreign_keys
// pragma) remove a site's crash groups/events when the site is deleted.
func TestCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustCreate(t, s, "jidlo", "Jidlo", "", false) // crash-only

	now := timeutil.Format(time.Now().UTC())
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO crash_group (site_id, fingerprint, title, level, count, status, first_seen, last_seen)
		 VALUES ('jidlo','fp1','boom','error',1,'open',?,?)`, now, now)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	gid, _ := res.LastInsertId()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO crash_event (site_id, group_id, level, message, occurred_at, received_at)
		 VALUES ('jidlo',?,'error','boom',?,?)`, gid, now, now); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	if _, _, err := s.Delete(ctx, "jidlo", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, tbl := range []string{"crash_group", "crash_event"} {
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tbl+" WHERE site_id = 'jidlo'").Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if n != 0 {
			t.Fatalf("expected cascade to remove %s rows, found %d", tbl, n)
		}
	}
}
