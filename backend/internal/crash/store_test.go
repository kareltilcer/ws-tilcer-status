package crash

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "crash.db"))
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
	if _, err := db.Exec(`INSERT INTO site (id, name, monitor_enabled, expected_status, crash_window_hours, ingest_key_hash, cached_color, created_at)
		VALUES ('home', 'Home', 0, 200, 24, 'h', 'unknown', ?)`, timeutil.Format(time.Now())); err != nil {
		t.Fatal(err)
	}
	return NewStore(db), db
}

func upsert(t *testing.T, s *Store, db *sql.DB, fp string, reopen bool) GroupUpsert {
	t.Helper()
	var g GroupUpsert
	if err := appdb.WithTx(context.Background(), db, func(tx *sql.Tx) error {
		var err error
		g, err = s.UpsertGroup(context.Background(), tx, "home", fp, "title", LevelError, timeutil.Format(time.Now()), reopen)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return g
}

func setStatus(t *testing.T, db *sql.DB, id int64, status string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE crash_group SET status = ? WHERE id = ?`, status, id); err != nil {
		t.Fatal(err)
	}
}

// TestUpsertGroupSaysWhatHappened: a notifier tells "a crash that came back"
// from "the 500th repeat" by Reopened alone, and reads the status it left.
func TestUpsertGroupSaysWhatHappened(t *testing.T) {
	s, db := newStore(t)

	first := upsert(t, s, db, "fp", true)
	if first.Reopened || first.Status != StatusOpen || first.ID == 0 {
		t.Fatalf("first event: %+v, want an open group", first)
	}
	again := upsert(t, s, db, "fp", true)
	if again.Reopened || again.ID != first.ID || again.Status != StatusOpen {
		t.Fatalf("repeat: %+v, want the same group, not reopened", again)
	}

	setStatus(t, db, first.ID, StatusResolved)
	back := upsert(t, s, db, "fp", true)
	if !back.Reopened || back.ID != first.ID || back.Status != StatusOpen {
		t.Fatalf("after resolve: %+v, want reopened", back)
	}

	setStatus(t, db, first.ID, StatusResolved)
	stays := upsert(t, s, db, "fp", false)
	if stays.Reopened || stays.Status != StatusResolved {
		t.Fatalf("reopen off: %+v, want still resolved", stays)
	}

	setStatus(t, db, first.ID, StatusIgnored)
	ignored := upsert(t, s, db, "fp", true)
	if ignored.Reopened || ignored.Status != StatusIgnored {
		t.Fatalf("ignored: %+v, an ignored group is never auto-reopened", ignored)
	}
}

// TestUpsertGroupReportsTheStoredTitle: with a `fingerprint` override, events
// with different messages share a group, and the group keeps its first title. A
// notifier names the crash by that title — the one the dashboard lists — not by
// whichever message came last.
func TestUpsertGroupReportsTheStoredTitle(t *testing.T) {
	s, db := newStore(t)
	up := func(title string) GroupUpsert {
		t.Helper()
		var g GroupUpsert
		if err := appdb.WithTx(context.Background(), db, func(tx *sql.Tx) error {
			var err error
			g, err = s.UpsertGroup(context.Background(), tx, "home", "db-timeout", title, LevelError, timeutil.Format(time.Now()), true)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return g
	}
	if g := up("Timeout on /api/a"); g.Title != "Timeout on /api/a" {
		t.Fatalf("new group title = %q", g.Title)
	}
	if g := up("Timeout on /api/b"); g.Title != "Timeout on /api/a" {
		t.Fatalf("repeat reported title %q, want the group's stored one", g.Title)
	}
}
