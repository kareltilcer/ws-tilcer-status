package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
)

func newSessionStore(t *testing.T) *SessionStore {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migFS, err := registry.MergeMigrations([]registry.MigrationSource{{Name: "platform", FS: appdb.MigrationsFS}})
	if err != nil {
		t.Fatalf("merge migrations: %v", err)
	}
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewSessionStore(db)
}

// insertSession writes a session row directly so a test can control expires_at
// exactly (including corrupt values the normal Create path would never produce).
func insertSession(t *testing.T, s *SessionStore, id, rawToken, expiresAt string, now time.Time) {
	t.Helper()
	ts := now.UTC().Format(tsLayout)
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO sessions
		   (id, user_id, token_hash, email, roles, roles_refreshed_at, created_at, last_seen_at, expires_at)
		 VALUES (?,?,?,?, '[]', ?,?,?,?)`,
		id, "user1", hashToken(rawToken), "u@x.cz", ts, ts, ts, expiresAt); err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
}

// TestLookupFailsClosedOnBadExpiry verifies a session whose expires_at is empty
// or unparseable is rejected (treated as expired), not accepted as never-expiring
// — a corrupt row must not become an immortal session.
func TestLookupFailsClosedOnBadExpiry(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// Control: a valid future expiry resolves to a live session.
	insertSession(t, s, "ok", "good-token", now.Add(time.Hour).UTC().Format(tsLayout), now)
	if _, live, err := s.Lookup(ctx, "good-token", now); err != nil || !live {
		t.Fatalf("valid session should be live: live=%v err=%v", live, err)
	}

	// An empty or unparseable expires_at must fail closed.
	for i, bad := range []string{"", "not-a-timestamp"} {
		id := "bad" + string(rune('0'+i))
		tok := "tok" + string(rune('0'+i))
		insertSession(t, s, id, tok, bad, now)
		if _, live, err := s.Lookup(ctx, tok, now); err != nil || live {
			t.Fatalf("session with expires_at=%q should be rejected, got live=%v err=%v", bad, live, err)
		}
	}
}
