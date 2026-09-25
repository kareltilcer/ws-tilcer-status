package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// querier is satisfied by *sql.Tx and *sql.DB, so the enqueue path reads and
// writes through the producer's transaction while the HTTP handlers and the
// worker use the pool.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// --- settings ---------------------------------------------------------------

type settings struct {
	Enabled    bool
	Recipients []string
	OnCrash    bool
	OnFeedback bool
	OnDowntime bool
	UpdatedAt  *string
}

func loadSettings(ctx context.Context, q querier) (settings, error) {
	var (
		s          settings
		recipients string
		updated    sql.NullString
	)
	err := q.QueryRowContext(ctx,
		`SELECT enabled, recipients, on_crash, on_feedback, on_downtime, updated_at
		   FROM notify_settings WHERE id = 1`).
		Scan(&s.Enabled, &recipients, &s.OnCrash, &s.OnFeedback, &s.OnDowntime, &updated)
	if err != nil {
		return settings{}, err
	}
	if err := json.Unmarshal([]byte(recipients), &s.Recipients); err != nil {
		return settings{}, err
	}
	if updated.Valid {
		v := updated.String
		s.UpdatedAt = &v
	}
	return s, nil
}

func saveSettings(ctx context.Context, q querier, s settings, updatedAt string) error {
	recipients, err := json.Marshal(nonNil(s.Recipients))
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx,
		`UPDATE notify_settings
		    SET enabled = ?, recipients = ?, on_crash = ?, on_feedback = ?, on_downtime = ?, updated_at = ?
		  WHERE id = 1`,
		s.Enabled, string(recipients), s.OnCrash, s.OnFeedback, s.OnDowntime, updatedAt)
	return err
}

// --- mutes ------------------------------------------------------------------

func isMuted(ctx context.Context, q querier, siteID string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM notify_site_mute WHERE site_id = ?`, siteID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// mutedSites returns the muted site ids, sorted. It drains its cursor before
// returning.
func mutedSites(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT site_id FROM notify_site_mute ORDER BY site_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func setMuted(ctx context.Context, q querier, siteID string, muted bool, now string) error {
	if muted {
		_, err := q.ExecContext(ctx,
			`INSERT INTO notify_site_mute (site_id, created_at) VALUES (?, ?) ON CONFLICT (site_id) DO NOTHING`,
			siteID, now)
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM notify_site_mute WHERE site_id = ?`, siteID)
	return err
}

// --- crash group state ------------------------------------------------------

// loadCrashState reads a group's arm state. A missing row is armed and never
// announced — see notify_crash_state.
func loadCrashState(ctx context.Context, q querier, groupID int64) (armed, announced bool, err error) {
	err = q.QueryRowContext(ctx,
		`SELECT armed, announced FROM notify_crash_state WHERE group_id = ?`, groupID).Scan(&armed, &announced)
	if err == sql.ErrNoRows {
		return true, false, nil
	}
	return armed, announced, err
}

func saveCrashState(ctx context.Context, q querier, groupID int64, armed, announced bool) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO notify_crash_state (group_id, armed, announced) VALUES (?, ?, ?)
		 ON CONFLICT (group_id) DO UPDATE SET armed = excluded.armed, announced = excluded.announced`,
		groupID, armed, announced)
	return err
}

// --- site downtime state ----------------------------------------------------

type siteState struct {
	DownSince string
	Announced bool
}

func loadSiteState(ctx context.Context, q querier, siteID string) (siteState, bool, error) {
	var st siteState
	err := q.QueryRowContext(ctx,
		`SELECT down_since, announced FROM notify_site_state WHERE site_id = ?`, siteID).
		Scan(&st.DownSince, &st.Announced)
	if err == sql.ErrNoRows {
		return siteState{}, false, nil
	}
	if err != nil {
		return siteState{}, false, err
	}
	return st, true, nil
}

func saveSiteDown(ctx context.Context, q querier, siteID, since string, announced bool) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO notify_site_state (site_id, down_since, announced) VALUES (?, ?, ?)
		 ON CONFLICT (site_id) DO UPDATE SET down_since = excluded.down_since, announced = excluded.announced`,
		siteID, since, announced)
	return err
}

func clearSiteDown(ctx context.Context, q querier, siteID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM notify_site_state WHERE site_id = ?`, siteID)
	return err
}

// --- outbox -----------------------------------------------------------------

func insertEvent(ctx context.Context, q querier, siteID, kind, ref string, p payload, createdAt string) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx,
		`INSERT INTO notify_event (site_id, kind, ref, payload, created_at) VALUES (?, ?, ?, ?, ?)`,
		siteID, kind, ref, string(b), createdAt)
	return err
}

// event is one outbox row as the worker reads it, with the site's CURRENT name —
// a site renamed between the event and the digest is mailed under its new name.
type event struct {
	ID        int64
	SiteID    string
	SiteName  string
	Kind      string
	Ref       string
	Payload   payload
	CreatedAt string
}

// oldestPending returns the created_at of the oldest event not yet in a digest.
func oldestPending(ctx context.Context, q querier) (string, bool, error) {
	var oldest sql.NullString
	if err := q.QueryRowContext(ctx,
		`SELECT MIN(created_at) FROM notify_event WHERE digest_id IS NULL`).Scan(&oldest); err != nil {
		return "", false, err
	}
	return oldest.String, oldest.Valid, nil
}

// pendingEvents reads up to limit unassigned events, oldest first, and drains the
// cursor before returning — the caller writes through the same transaction next.
func pendingEvents(ctx context.Context, q querier, limit int) ([]event, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT e.id, e.site_id, s.name, e.kind, e.ref, e.payload, e.created_at
		   FROM notify_event e JOIN site s ON s.id = e.site_id
		  WHERE e.digest_id IS NULL
		  ORDER BY e.id
		  LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var (
			e   event
			raw string
		)
		if err := rows.Scan(&e.ID, &e.SiteID, &e.SiteName, &e.Kind, &e.Ref, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func deleteEvents(ctx context.Context, q querier, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.ExecContext(ctx, `DELETE FROM notify_event WHERE id IN (`+placeholders(len(ids))+`)`, int64Args(ids)...)
	return err
}

func assignEvents(ctx context.Context, q querier, digestID int64, ids []int64) error {
	args := append([]any{digestID}, int64Args(ids)...)
	_, err := q.ExecContext(ctx,
		`UPDATE notify_event SET digest_id = ? WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// --- digests ----------------------------------------------------------------

type digest struct {
	ID             int64
	CreatedAt      string
	Sender         string
	Recipients     []string
	Subject        string
	Text           string
	HTML           string
	EventCount     int
	IdempotencyKey string
	State          string
	Attempts       int
	NextAttemptAt  string
	LastError      *string
	SentAt         *string
}

func digestsSince(ctx context.Context, q querier, since string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_digest WHERE created_at > ?`, since).Scan(&n)
	return n, err
}

func insertDigest(ctx context.Context, q querier, d digest) (int64, error) {
	recipients, err := json.Marshal(nonNil(d.Recipients))
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO notify_digest
		   (created_at, sender, recipients, subject, body_text, body_html, event_count,
		    idempotency_key, state, attempts, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?)`,
		d.CreatedAt, d.Sender, string(recipients), d.Subject, d.Text, d.HTML, d.EventCount,
		d.IdempotencyKey, d.NextAttemptAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const digestCols = `id, created_at, sender, recipients, subject, body_text, body_html, event_count,
	idempotency_key, state, attempts, next_attempt_at, last_error, sent_at`

func scanDigest(rows *sql.Rows) (digest, error) {
	var (
		d               digest
		recipients      string
		lastErr, sentAt sql.NullString
	)
	if err := rows.Scan(&d.ID, &d.CreatedAt, &d.Sender, &recipients, &d.Subject, &d.Text, &d.HTML, &d.EventCount,
		&d.IdempotencyKey, &d.State, &d.Attempts, &d.NextAttemptAt, &lastErr, &sentAt); err != nil {
		return digest{}, err
	}
	if err := json.Unmarshal([]byte(recipients), &d.Recipients); err != nil {
		return digest{}, err
	}
	d.LastError = nsPtr(lastErr)
	d.SentAt = nsPtr(sentAt)
	return d, nil
}

// dueDigests returns up to limit pending digests whose next attempt is due,
// oldest first. ⚠ It drains and closes its cursor before returning: the caller
// sends each one next, and a send with a cursor open holds the only connection.
func dueDigests(ctx context.Context, q querier, now string, limit int) ([]digest, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+digestCols+` FROM notify_digest
		  WHERE state = 'pending' AND next_attempt_at <= ?
		  ORDER BY next_attempt_at, id
		  LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []digest
	for rows.Next() {
		d, err := scanDigest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// recentDigests returns the newest digests for the deliveries list.
func recentDigests(ctx context.Context, q querier, limit int) ([]digest, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+digestCols+` FROM notify_digest ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []digest{}
	for rows.Next() {
		d, err := scanDigest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// The three outcomes of a send. Each is one statement guarded by
// state = 'pending', so a digest settled by anything else is never overwritten.

func markSent(ctx context.Context, q querier, id int64, providerID, at string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE notify_digest
		    SET state = 'sent', attempts = attempts + 1, last_attempt_at = ?, sent_at = ?,
		        provider_message_id = ?, last_error = NULL
		  WHERE id = ? AND state = 'pending'`,
		at, at, nullIfEmpty(providerID), id)
	return err
}

func markRetry(ctx context.Context, q querier, id int64, lastErr, at, next string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE notify_digest
		    SET attempts = attempts + 1, last_attempt_at = ?, last_error = ?, next_attempt_at = ?
		  WHERE id = ? AND state = 'pending'`,
		at, lastErr, next, id)
	return err
}

// markFailed gives up on a digest. attempted says whether this call follows a
// send (a refusal) or replaces one (expiry, cancellation) — only the former
// counts as an attempt.
func markFailed(ctx context.Context, q querier, id int64, lastErr, at string, attempted bool) error {
	inc := 0
	if attempted {
		inc = 1
	}
	_, err := q.ExecContext(ctx,
		`UPDATE notify_digest
		    SET state = 'failed', attempts = attempts + ?, last_attempt_at = COALESCE(?, last_attempt_at), last_error = ?
		  WHERE id = ? AND state = 'pending'`,
		inc, attemptAt(attempted, at), lastErr, id)
	return err
}

// cancelPending fails every pending digest — notifications were switched off, and
// a queue that resumes delivering yesterday's news the moment they are switched
// back on is not what "off" means.
func cancelPending(ctx context.Context, q querier, reason string) (int64, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE notify_digest SET state = 'failed', last_error = ? WHERE state = 'pending'`, reason)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- retention --------------------------------------------------------------

// prune deletes digests (and, by cascade, their events) older than cutoff, any
// unassigned event that old, and the downtime memory of sites that are no longer
// monitored — a site re-enabled weeks later must not open with a "back up" email
// about an outage nobody was watching.
//
// A digest that old is deleted whatever its state: past giveUpAfter it can no
// longer be sent anyway, and one left pending because the worker stopped running
// (the key was removed) would otherwise sit on the deliveries list forever.
func prune(ctx context.Context, q querier, cutoff string) (digests, events, states int64, err error) {
	res, err := q.ExecContext(ctx, `DELETE FROM notify_digest WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, 0, 0, err
	}
	digests, _ = res.RowsAffected()
	res, err = q.ExecContext(ctx,
		`DELETE FROM notify_event WHERE digest_id IS NULL AND created_at < ?`, cutoff)
	if err != nil {
		return digests, 0, 0, err
	}
	events, _ = res.RowsAffected()
	res, err = q.ExecContext(ctx,
		`DELETE FROM notify_site_state
		  WHERE site_id IN (SELECT id FROM site WHERE monitor_enabled = 0)`)
	if err != nil {
		return digests, events, 0, err
	}
	states, _ = res.RowsAffected()
	return digests, events, states, nil
}

// --- helpers ----------------------------------------------------------------

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nsPtr(ns sql.NullString) *string {
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

func attemptAt(attempted bool, at string) any {
	if !attempted {
		return nil
	}
	return at
}

// ts is the canonical stored form of a time.
var ts = timeutil.Format
