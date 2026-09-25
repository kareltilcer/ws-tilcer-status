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

// TestUpsertGroupSaysWhatHappened: a notifier tells "a new crash" and "a crash
// that came back" from "the 500th repeat" by these flags alone.
func TestUpsertGroupSaysWhatHappened(t *testing.T) {
	s, db := newStore(t)

	first := upsert(t, s, db, "fp", true)
	if !first.Created || first.Reopened || first.Status != StatusOpen || first.ID == 0 {
		t.Fatalf("first event: %+v, want created and open", first)
	}
	again := upsert(t, s, db, "fp", true)
	if again.Created || again.Reopened || again.ID != first.ID || again.Status != StatusOpen {
		t.Fatalf("repeat: %+v, want neither created nor reopened", again)
	}

	setStatus(t, db, first.ID, StatusResolved)
	back := upsert(t, s, db, "fp", true)
	if !back.Reopened || back.Created || back.Status != StatusOpen {
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
