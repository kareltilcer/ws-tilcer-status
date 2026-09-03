package retention

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// TestRetentionDoesNotPurgeFeedback exists because the purge is a natural place
// for a later contributor to add a fourth table by symmetry (V3-D28).
//
// ⚠ RETENTION_DAYS purges check_result and crash_event. A report is a
// hand-written artifact — someone sat down and described a problem — and is kept
// until it is deliberately deleted, however old it is.
func TestRetentionDoesNotPurgeFeedback(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "ret-feedback.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migFS, err := registry.MergeMigrations([]registry.MigrationSource{
		{Name: "sites", FS: sites.MigrationsFS},
		{Name: "feedback", FS: feedback.MigrationsFS},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	ancient := timeutil.Format(now.AddDate(-3, 0, 0)) // three years old, far beyond any window

	exec := func(q string, args ...any) {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(`INSERT INTO site (id,name,monitor_enabled,expected_status,crash_window_hours,ingest_key_hash,cached_color,fail_streak,created_at)
	      VALUES ('home','Home',0,200,24,'h','green',0,?)`, ancient)
	exec(`INSERT INTO feedback_report (ref,site_id,kind,message,state,created_at,updated_at)
	      VALUES ('R-7QK2','home','bug','the board does not load','new',?,?)`, ancient, ancient)
	exec(`INSERT INTO feedback_attachment (report_id,object_key,content_type,byte_size,state,created_at)
	      VALUES (1,'feedback/home/R-7QK2/0-abc.png','image/png',1024,'stored',?)`, ancient)
	exec(`INSERT INTO feedback_site_config (site_id,enabled,widget_key_hash,widget_key_set_at,console_capture,created_at,updated_at)
	      VALUES ('home',1,'hash',?,0,?,?)`, ancient, ancient, ancient)

	p := NewPurger(db, 90, 400, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.Purge(ctx, now); err != nil {
		t.Fatalf("purge: %v", err)
	}

	for _, table := range []string{"feedback_report", "feedback_attachment", "feedback_site_config"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 1 {
			t.Fatalf("retention purged %s (%d rows left) — reports are kept until deleted, whatever RETENTION_DAYS says", table, n)
		}
	}
}
