package retention

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

func TestPurgeBoundaries(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "ret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migFS, _ := registry.MergeMigrations([]registry.MigrationSource{{Name: "sites", FS: sites.MigrationsFS}})
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	ts := func(daysAgo int) string { return timeutil.Format(now.AddDate(0, 0, -daysAgo)) }
	day := func(daysAgo int) string { return timeutil.Day(now.AddDate(0, 0, -daysAgo)) }

	exec := func(q string, args ...any) {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(`INSERT INTO site (id,name,monitor_url,monitor_enabled,expected_status,crash_window_hours,ingest_key_hash,cached_color,fail_streak,created_at)
	      VALUES ('fin','Finance','https://fin/readyz',1,200,24,'h','green',0,?)`, ts(200))

	// check_result: one old (purged >90d), one recent (kept).
	exec(`INSERT INTO check_result (site_id,checked_at,ok) VALUES ('fin',?,1)`, ts(100))
	exec(`INSERT INTO check_result (site_id,checked_at,ok) VALUES ('fin',?,1)`, ts(1))

	// check_rollup: one within rollup retention (kept), one beyond 400d (purged).
	exec(`INSERT INTO check_rollup (site_id,day,ok_count,fail_count) VALUES ('fin',?,280,8)`, day(100))
	exec(`INSERT INTO check_rollup (site_id,day,ok_count,fail_count) VALUES ('fin',?,280,8)`, day(500))

	// group A: only an old event (event purged → group becomes empty → removed).
	var gidA int64
	res, _ := db.ExecContext(ctx, `INSERT INTO crash_group (site_id,fingerprint,title,level,count,status,first_seen,last_seen) VALUES ('fin','a','old','error',1,'open',?,?)`, ts(100), ts(100))
	gidA, _ = res.LastInsertId()
	exec(`INSERT INTO crash_event (site_id,group_id,level,message,occurred_at,received_at) VALUES ('fin',?,'error','old',?,?)`, gidA, ts(100), ts(100))

	// group B: a recent event (kept).
	res, _ = db.ExecContext(ctx, `INSERT INTO crash_group (site_id,fingerprint,title,level,count,status,first_seen,last_seen) VALUES ('fin','b','new','error',1,'open',?,?)`, ts(1), ts(1))
	gidB, _ := res.LastInsertId()
	exec(`INSERT INTO crash_event (site_id,group_id,level,message,occurred_at,received_at) VALUES ('fin',?,'error','new',?,?)`, gidB, ts(1), ts(1))

	p := NewPurger(db, 90, 400, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.Purge(ctx, now); err != nil {
		t.Fatalf("purge: %v", err)
	}

	count := func(q string, args ...any) int {
		var n int
		if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM check_result`); n != 1 {
		t.Fatalf("check_result: want 1 kept, got %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM check_rollup`); n != 1 {
		t.Fatalf("check_rollup: want 1 kept (100d survives 400d retention), got %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM crash_event`); n != 1 {
		t.Fatalf("crash_event: want 1 kept, got %d", n)
	}
	// group A (now empty) removed; group B kept.
	if n := count(`SELECT COUNT(*) FROM crash_group WHERE id=?`, gidA); n != 0 {
		t.Fatalf("empty group A should be removed")
	}
	if n := count(`SELECT COUNT(*) FROM crash_group WHERE id=?`, gidB); n != 1 {
		t.Fatalf("group B with a recent event should be kept")
	}
	// The 100-day rollup survives the 90-day raw purge → 90d uptime still renders.
	if n := count(`SELECT COUNT(*) FROM check_rollup WHERE day=?`, day(100)); n != 1 {
		t.Fatalf("100-day rollup must survive the raw purge")
	}
}
