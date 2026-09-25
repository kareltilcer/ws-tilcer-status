package crash

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/paging"
)

// Store is the crash module persistence layer.
type Store struct{ db *sql.DB }

// NewStore returns a store over db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// GroupUpsert is what UpsertGroup did: the group's id, its status after this
// event, and whether this event brought it back from resolved. A notifier needs
// to tell "a crash that came back" from "the 500th repeat", and only the upsert
// knows which one happened. ("New" is not here on purpose — see Signal.)
type GroupUpsert struct {
	ID       int64
	Reopened bool
	Status   string // the group's status after this event
	// Title is the group's STORED title — the first event's — which is what the
	// dashboard lists it under. With a `fingerprint` override a later event's
	// message can differ from it.
	Title string
}

// UpsertGroup creates or bumps the crash group for (siteID, fingerprint) within
// tx. On a hit it increments count, widens first/last seen, raises the tracked
// level to the highest seen, and reopens a resolved group when reopen is set (an
// ignored group is never auto-reopened).
func (s *Store) UpsertGroup(ctx context.Context, tx *sql.Tx, siteID, fingerprint, title, level, at string, reopen bool) (GroupUpsert, error) {
	var (
		id       int64
		status   string
		curLevel string
		curTitle string
	)
	err := tx.QueryRowContext(ctx,
		`SELECT id, status, level, title FROM crash_group WHERE site_id = ? AND fingerprint = ?`,
		siteID, fingerprint).Scan(&id, &status, &curLevel, &curTitle)
	if err == sql.ErrNoRows {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO crash_group (site_id, fingerprint, title, level, count, status, first_seen, last_seen)
			 VALUES (?,?,?,?,1,'open',?,?)`,
			siteID, fingerprint, title, level, at, at)
		if err != nil {
			return GroupUpsert{}, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return GroupUpsert{}, err
		}
		return GroupUpsert{ID: id, Status: StatusOpen, Title: title}, nil
	}
	if err != nil {
		return GroupUpsert{}, err
	}
	newStatus := status
	if status == StatusResolved && reopen {
		newStatus = StatusOpen
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE crash_group
		    SET count = count + 1,
		        last_seen = MAX(last_seen, ?),
		        first_seen = MIN(first_seen, ?),
		        level = ?,
		        status = ?
		  WHERE id = ?`,
		at, at, maxLevel(curLevel, level), newStatus, id); err != nil {
		return GroupUpsert{}, err
	}
	return GroupUpsert{ID: id, Reopened: newStatus != status, Status: newStatus, Title: curTitle}, nil
}

// InsertEvent inserts one crash event within tx and returns its id.
func (s *Store) InsertEvent(ctx context.Context, tx *sql.Tx, siteID string, groupID int64,
	level, message, stack, environment, release, contextJSON, occurredAt, receivedAt string) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO crash_event
		   (site_id, group_id, level, message, stack, environment, release, context, occurred_at, received_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		siteID, groupID, level, message,
		nullIfEmpty(stack), nullIfEmpty(environment), nullIfEmpty(release), nullIfEmpty(contextJSON),
		occurredAt, receivedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const groupCols = `id, site_id, fingerprint, title, level, count, status, first_seen, last_seen`

func scanGroup(row interface{ Scan(...any) error }) (CrashGroup, error) {
	var g CrashGroup
	err := row.Scan(&g.ID, &g.SiteID, &g.Fingerprint, &g.Title, &g.Level, &g.Count, &g.Status, &g.FirstSeen, &g.LastSeen)
	return g, err
}

// ListGroups returns a page of crash groups for a site, newest activity first,
// optionally filtered by status and level.
func (s *Store) ListGroups(ctx context.Context, siteID, statusFilter, levelFilter string, limit int, cursor string) (GroupPage, error) {
	filters := []string{"site_id = ?"}
	args := []any{siteID}
	if statusFilter != "" {
		filters = append(filters, "status = ?")
		args = append(args, statusFilter)
	}
	if levelFilter != "" {
		filters = append(filters, "level = ?")
		args = append(args, levelFilter)
	}
	items, next, err := paging.Paginate(ctx, s.db, limit, cursor, paging.Query[CrashGroup]{
		Columns:   groupCols,
		From:      "crash_group",
		Filters:   filters,
		Args:      args,
		CursorCol: "last_seen",
		IDCol:     "id",
		Scan:      func(rows *sql.Rows) (CrashGroup, error) { return scanGroup(rows) },
		Key:       func(g CrashGroup) (string, int64) { return g.LastSeen, g.ID },
	})
	if err != nil {
		if errors.Is(err, paging.ErrInvalidCursor) {
			return GroupPage{}, errInvalidCursor
		}
		return GroupPage{}, err
	}
	return GroupPage{Items: items, NextCursor: next}, nil
}

// GetGroup returns one group, or (nil, nil) when it does not exist.
func (s *Store) GetGroup(ctx context.Context, groupID int64) (*CrashGroup, error) {
	g, err := scanGroup(s.db.QueryRowContext(ctx, "SELECT "+groupCols+" FROM crash_group WHERE id = ?", groupID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

const eventCols = `id, group_id, site_id, level, message, stack, environment, release, context, occurred_at, received_at`

// ListEvents returns a page of events for a group, newest first.
func (s *Store) ListEvents(ctx context.Context, groupID int64, limit int, cursor string) ([]CrashEvent, *string, error) {
	items, next, err := paging.Paginate(ctx, s.db, limit, cursor, paging.Query[CrashEvent]{
		Columns:   eventCols,
		From:      "crash_event",
		Filters:   []string{"group_id = ?"},
		Args:      []any{groupID},
		CursorCol: "occurred_at",
		IDCol:     "id",
		Scan:      scanEvent,
		Key:       func(e CrashEvent) (string, int64) { return e.OccurredAt, e.ID },
	})
	if err != nil {
		if errors.Is(err, paging.ErrInvalidCursor) {
			return nil, nil, errInvalidCursor
		}
		return nil, nil, err
	}
	return items, next, nil
}

func scanEvent(rows *sql.Rows) (CrashEvent, error) {
	var (
		e                            CrashEvent
		stack, env, rel, contextJSON sql.NullString
	)
	if err := rows.Scan(&e.ID, &e.GroupID, &e.SiteID, &e.Level, &e.Message,
		&stack, &env, &rel, &contextJSON, &e.OccurredAt, &e.ReceivedAt); err != nil {
		return CrashEvent{}, err
	}
	e.Stack = nsToPtr(stack)
	e.Environment = nsToPtr(env)
	e.Release = nsToPtr(rel)
	if contextJSON.Valid && contextJSON.String != "" {
		_ = json.Unmarshal([]byte(contextJSON.String), &e.Context)
	}
	return e, nil
}

// SetGroupStatus updates a group's triage status within tx and returns its site
// id (needed to recompute the site's color). found is false when the group does
// not exist.
func (s *Store) SetGroupStatus(ctx context.Context, tx *sql.Tx, groupID int64, status string) (siteID string, found bool, err error) {
	err = tx.QueryRowContext(ctx, "SELECT site_id FROM crash_group WHERE id = ?", groupID).Scan(&siteID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE crash_group SET status = ? WHERE id = ?", status, groupID); err != nil {
		return "", false, err
	}
	return siteID, true, nil
}

// --- helpers ---

func nsToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
