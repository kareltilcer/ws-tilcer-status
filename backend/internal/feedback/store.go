package feedback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/paging"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// errInvalidCursor is returned when a pagination cursor is malformed (→ 422).
var errInvalidCursor = errors.New("feedback: invalid cursor")

// errRefExhausted means ref generation lost the collision race refAttempts times
// in a row, which at 32^4 values and a household's volume means something else is
// wrong.
var errRefExhausted = errors.New("feedback: could not allocate a unique ref")

// errConfigMissing is UpsertConfig refusing to CREATE a configuration row for a
// request that is not enabling feedback. The row cannot exist without a widget
// key, and a key is shown exactly once — so a request that would create one as a
// side effect of changing some other setting is refused rather than served with
// a key nobody asked for and nobody will see again.
var errConfigMissing = errors.New("feedback: site has no feedback configuration")

// refAttempts is how many times a report insert retries on the unique(ref)
// violation. Generating and retrying beats pre-checking, which is a race.
const refAttempts = 5

// Store is the feedback module's persistence layer.
//
// ⚠ Nothing here talks to object storage. Every method that feeds an R2 call
// returns the keys and lets the caller issue the request after the transaction
// commits and the cursor is closed (V3-D05a) — the pool is one connection, and a
// network round-trip inside a transaction stalls every other write for the
// length of someone else's TCP timeout.
type Store struct{ db *sql.DB }

// NewStore returns a store over db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// --- per-site configuration -------------------------------------------------

// siteConfig is the stored per-site configuration.
type siteConfig struct {
	SiteID         string
	Enabled        bool
	WidgetKeyHash  string
	WidgetKeySetAt string
	ConsoleCapture bool
	UpdatedAt      string
}

// Config returns a site's configuration, or (nil, nil) when it has no row. A
// site with no row has feedback off; absence is the default state, not a missing
// row to repair (V3-D03).
func (s *Store) Config(ctx context.Context, siteID string) (*siteConfig, error) {
	var c siteConfig
	err := s.db.QueryRowContext(ctx,
		`SELECT site_id, enabled, widget_key_hash, widget_key_set_at, console_capture, updated_at
		   FROM feedback_site_config WHERE site_id = ?`, siteID).
		Scan(&c.SiteID, &c.Enabled, &c.WidgetKeyHash, &c.WidgetKeySetAt, &c.ConsoleCapture, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpsertConfig applies a patch, creating the row when it is absent. It returns
// the resulting configuration and the plaintext of the widget key, which is
// non-empty on exactly the request that created the row — the one request that
// will ever be able to show it.
//
// ⚠ `mintKey` is a minter rather than a key, and nil means "this request may not
// create the row" (the old `allowCreate`). Both halves matter. Creating the row
// mints the widget key and a key is shown exactly once, in the response to the
// request that minted it, so only a request that turns feedback ON may create it
// (FR-14) — and a request that CAN create the row still only mints when it
// actually does, which a pre-minted key handed in from outside could not express:
// every enable of an already-configured site generated a secret and threw it
// away, leaving "was a key issued here?" with one more thing to rule out.
//
// The read and the write share one transaction: BeginTx issues BEGIN IMMEDIATE
// here (the DSN's _txlock), so two concurrent patches cannot both find the row
// absent and both try to insert it.
func (s *Store) UpsertConfig(ctx context.Context, siteID string, enabled, console *bool, mintKey func() (plaintext, hash string, err error), now time.Time) (out *siteConfig, issued string, err error) {
	ts := timeutil.Format(now)
	err = appdb.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var c siteConfig
		scanErr := tx.QueryRowContext(ctx,
			`SELECT site_id, enabled, widget_key_hash, widget_key_set_at, console_capture, updated_at
			   FROM feedback_site_config WHERE site_id = ?`, siteID).
			Scan(&c.SiteID, &c.Enabled, &c.WidgetKeyHash, &c.WidgetKeySetAt, &c.ConsoleCapture, &c.UpdatedAt)
		switch {
		case errors.Is(scanErr, sql.ErrNoRows):
			// ⚠ The decision is made here, inside the transaction that would do
			// the writing, rather than by reading the row first and deciding
			// outside it — and so, now, is the minting.
			if mintKey == nil {
				return errConfigMissing
			}
			plaintext, keyHash, mintErr := mintKey()
			if mintErr != nil {
				return mintErr
			}
			c = siteConfig{SiteID: siteID, WidgetKeyHash: keyHash, WidgetKeySetAt: ts, UpdatedAt: ts}
			if enabled != nil {
				c.Enabled = *enabled
			}
			if console != nil {
				c.ConsoleCapture = *console
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO feedback_site_config
				   (site_id, enabled, widget_key_hash, widget_key_set_at, console_capture, created_at, updated_at)
				 VALUES (?,?,?,?,?,?,?)`,
				c.SiteID, c.Enabled, c.WidgetKeyHash, c.WidgetKeySetAt, c.ConsoleCapture, ts, ts); err != nil {
				return err
			}
			out, issued = &c, plaintext
			return nil
		case scanErr != nil:
			return scanErr
		}
		if enabled != nil {
			c.Enabled = *enabled
		}
		if console != nil {
			c.ConsoleCapture = *console
		}
		c.UpdatedAt = ts
		if _, err := tx.ExecContext(ctx,
			`UPDATE feedback_site_config SET enabled = ?, console_capture = ?, updated_at = ? WHERE site_id = ?`,
			c.Enabled, c.ConsoleCapture, ts, siteID); err != nil {
			return err
		}
		out = &c
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return out, issued, nil
}

// SetWidgetKeyHash rotates the key on an existing row, or creates the row
// (disabled) when there is none.
//
// ⚠ It never touches site.ingest_key_hash: rotating a spammed widget key must not
// silence that site's crash reporting, which is the whole reason the two keys are
// separate.
func (s *Store) SetWidgetKeyHash(ctx context.Context, siteID, hash string, now time.Time) error {
	ts := timeutil.Format(now)
	// ⚠ UPDATE-then-INSERT is two statements deciding one outcome, so they run in
	// one transaction — as UpsertConfig's does for the same shape. The single
	// writer connection makes the interleaving unlikely rather than impossible,
	// and the failure it prevents is the expensive kind: a rotate that updates
	// nothing and then collides on the primary key would answer 500 having
	// already thrown away the plaintext of the key it minted.
	return appdb.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE feedback_site_config SET widget_key_hash = ?, widget_key_set_at = ?, updated_at = ? WHERE site_id = ?`,
			hash, ts, ts, siteID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		// No configuration yet: create it disabled, so rotating a key never turns
		// feedback on as a side effect.
		_, err = tx.ExecContext(ctx,
			`INSERT INTO feedback_site_config
			   (site_id, enabled, widget_key_hash, widget_key_set_at, console_capture, created_at, updated_at)
			 VALUES (?,0,?,?,0,?,?)`,
			siteID, hash, ts, ts, ts)
		return err
	})
}

// --- tickets ----------------------------------------------------------------

// InsertTicket records an issued ticket so it can be spent exactly once.
func (s *Store) InsertTicket(ctx context.Context, t ticket, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback_ticket (id, site_id, issued_at, expires_at) VALUES (?,?,?,?)`,
		t.ID, t.SiteID, timeutil.Format(t.IssuedAt), timeutil.Format(expiresAt))
	return err
}

// SpendTicket consumes a ticket, returning false when it does not exist or was
// already spent. The row is deleted rather than flagged: single use is then a
// property of the database, not of a code path that has to remember.
func (s *Store) SpendTicket(ctx context.Context, id, siteID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM feedback_ticket WHERE id = ? AND site_id = ?`, id, siteID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PurgeExpiredTickets drops tickets past their expiry (the sweep's cheapest step).
func (s *Store) PurgeExpiredTickets(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM feedback_ticket WHERE expires_at < ?`, timeutil.Format(now))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// --- reports ----------------------------------------------------------------

// newReport is a validated report ready to insert.
type newReport struct {
	SiteID        string
	Kind          string
	Message       string
	ReporterLabel *string
	PageURL       *string
	Referrer      *string
	UserAgent     *string
	Viewport      *string
	Locale        *string
	AppRelease    *string
	ConsoleTail   []string
	LastError     *string
	IPHash        *string
}

// slot is one declared file resolved against the allow-list and the caps: the
// object key, the type that will be signed, and the size the upload URL will be
// signed for (already clamped).
type slot struct {
	AttachmentID int64
	ObjectKey    string
	ContentType  string
	ByteSize     int64
}

// onInsert runs inside the insert transaction, after the report and its
// attachment rows and before the commit, with the ref this attempt minted. A ref
// collision rolls it back with everything else, and the retry runs it again.
type onInsert func(ctx context.Context, tx *sql.Tx, ref string) error

// InsertReport writes the report and one pending attachment row per declared file
// in a single transaction, retrying on the unique(ref) collision. Object keys are
// derived from the ref, so they are minted here; nothing in this method touches
// object storage. hook may be nil.
func (s *Store) InsertReport(ctx context.Context, r newReport, files []resolvedFile, now time.Time, hook onInsert) (ref string, slots []slot, err error) {
	ts := timeutil.Format(now)
	var consoleJSON *string
	if len(r.ConsoleTail) > 0 {
		b, err := json.Marshal(r.ConsoleTail)
		if err != nil {
			return "", nil, err
		}
		enc := string(b)
		consoleJSON = &enc
	}

	for attempt := 0; attempt < refAttempts; attempt++ {
		candidate, err := NewRef()
		if err != nil {
			return "", nil, err
		}
		out, err := s.insertReportOnce(ctx, r, files, candidate, consoleJSON, ts, hook)
		if err != nil {
			if isUniqueViolation(err) {
				continue // lost the ref race; mint another
			}
			return "", nil, err
		}
		return candidate, out, nil
	}
	return "", nil, errRefExhausted
}

// insertReportOnce is one attempt of InsertReport, in one transaction.
func (s *Store) insertReportOnce(ctx context.Context, r newReport, files []resolvedFile, ref string, consoleJSON *string, ts string, hook onInsert) ([]slot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	res, err := tx.ExecContext(ctx,
		`INSERT INTO feedback_report
		   (ref, site_id, kind, message, state, reporter_label, page_url, referrer, user_agent,
		    viewport, locale, app_release, console_tail, last_error, ip_hash, created_at, updated_at)
		 VALUES (?,?,?,?,'new',?,?,?,?,?,?,?,?,?,?,?,?)`,
		ref, r.SiteID, r.Kind, r.Message, r.ReporterLabel, r.PageURL, r.Referrer, r.UserAgent,
		r.Viewport, r.Locale, r.AppRelease, consoleJSON, r.LastError, r.IPHash, ts, ts)
	if err != nil {
		return nil, err
	}
	reportID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	slots := make([]slot, 0, len(files))
	for i, f := range files {
		key, err := objectKey(r.SiteID, ref, i, f.Type.ext)
		if err != nil {
			return nil, err
		}
		ares, err := tx.ExecContext(ctx,
			`INSERT INTO feedback_attachment (report_id, object_key, content_type, byte_size, state, created_at)
			 VALUES (?,?,?,?,'pending',?)`,
			reportID, key, f.Type.contentType, f.SignedSize, ts)
		if err != nil {
			return nil, err
		}
		aid, err := ares.LastInsertId()
		if err != nil {
			return nil, err
		}
		slots = append(slots, slot{AttachmentID: aid, ObjectKey: key, ContentType: f.Type.contentType, ByteSize: f.SignedSize})
	}
	// ⚠ Last before the commit — see onInsert and Notifier.
	if hook != nil {
		if err := hook(ctx, tx, ref); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return slots, nil
}

// reportRow is a ReportSummary plus the internal id the keyset cursor tie-breaks
// on. The id is never serialized — the ref is the report's public name.
type reportRow struct {
	ReportSummary
	id int64
}

const reportSummaryCols = `r.id, r.ref, r.site_id, r.kind, r.state, r.message, r.reporter_label, r.created_at, r.updated_at,
	(SELECT COUNT(*) FROM feedback_attachment a WHERE a.report_id = r.id) AS attachment_count`

// summaryMessageRunes is how much of a report's text the inbox carries.
// openapi's ReportSummary.message is "Truncated in the list; the detail carries
// the whole text" — a row shows a first line, and a page of 50 four-thousand
// character reports is a payload nothing renders.
const summaryMessageRunes = 200

func scanReportRow(rows *sql.Rows) (reportRow, error) {
	var (
		out   reportRow
		label sql.NullString
	)
	err := rows.Scan(&out.id, &out.Ref, &out.SiteID, &out.Kind, &out.State, &out.Message, &label,
		&out.CreatedAt, &out.UpdatedAt, &out.AttachmentCount)
	out.Message = truncateRunes(out.Message, summaryMessageRunes)
	out.ReporterLabel = nsToPtr(label)
	return out, err
}

// ListReports returns a page of the cross-site inbox, newest first, keyset
// paginated over (created_at, id).
//
// ⚠ The cursor is a valid order only because every timestamp uses
// timeutil.Layout, whose fixed width makes string comparison a time comparison.
func (s *Store) ListReports(ctx context.Context, state, siteID, kind string, limit int, cursor string) (ReportPage, error) {
	var filters []string
	var args []any
	if state != "" {
		filters = append(filters, "r.state = ?")
		args = append(args, state)
	}
	if siteID != "" {
		filters = append(filters, "r.site_id = ?")
		args = append(args, siteID)
	}
	if kind != "" {
		filters = append(filters, "r.kind = ?")
		args = append(args, kind)
	}
	rows, next, err := paging.Paginate(ctx, s.db, limit, cursor, paging.Query[reportRow]{
		Columns:   reportSummaryCols,
		From:      "feedback_report r",
		Filters:   filters,
		Args:      args,
		CursorCol: "r.created_at",
		IDCol:     "r.id",
		Scan:      scanReportRow,
		Key:       func(x reportRow) (string, int64) { return x.CreatedAt, x.id },
	})
	if err != nil {
		if errors.Is(err, paging.ErrInvalidCursor) {
			return ReportPage{}, errInvalidCursor
		}
		return ReportPage{}, err
	}
	items := make([]ReportSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.ReportSummary)
	}
	return ReportPage{Items: items, NextCursor: next}, nil
}

// Get returns one report with its attachments, or (nil, nil) when unknown. The
// attachment query runs only after the report row is fully read: with a single
// connection, querying inside an open cursor deadlocks.
func (s *Store) Get(ctx context.Context, ref string) (*Report, error) {
	var (
		out                                    Report
		id                                     int64
		label, pageURL, referrer, ua, viewport sql.NullString
		locale, release, console, lastErr      sql.NullString
		note, resolved                         sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT r.id, r.ref, r.site_id, r.kind, r.state, r.message, r.reporter_label, r.page_url, r.referrer,
		        r.user_agent, r.viewport, r.locale, r.app_release, r.console_tail, r.last_error, r.internal_note,
		        r.created_at, r.updated_at, r.resolved_at
		   FROM feedback_report r WHERE r.ref = ?`, ref).
		Scan(&id, &out.Ref, &out.SiteID, &out.Kind, &out.State, &out.Message, &label, &pageURL, &referrer,
			&ua, &viewport, &locale, &release, &console, &lastErr, &note,
			&out.CreatedAt, &out.UpdatedAt, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.ReporterLabel = nsToPtr(label)
	out.PageURL = nsToPtr(pageURL)
	out.Referrer = nsToPtr(referrer)
	out.UserAgent = nsToPtr(ua)
	out.Viewport = nsToPtr(viewport)
	out.Locale = nsToPtr(locale)
	out.AppRelease = nsToPtr(release)
	out.LastError = nsToPtr(lastErr)
	out.InternalNote = nsToPtr(note)
	out.ResolvedAt = nsToPtr(resolved)
	if console.Valid && console.String != "" {
		var lines []string
		if err := json.Unmarshal([]byte(console.String), &lines); err == nil {
			out.ConsoleTail = lines
		}
	}
	att, err := s.attachments(ctx, id)
	if err != nil {
		return nil, err
	}
	out.Attachments = att
	out.AttachmentCount = len(att)
	return &out, nil
}

// attachments returns one report's attachments, oldest first.
func (s *Store) attachments(ctx context.Context, reportID int64) ([]AttachmentSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, state, content_type, byte_size, created_at FROM feedback_attachment
		  WHERE report_id = ? ORDER BY id ASC`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AttachmentSummary{}
	for rows.Next() {
		var (
			a    AttachmentSummary
			size int64
		)
		if err := rows.Scan(&a.ID, &a.State, &a.ContentType, &size, &a.CreatedAt); err != nil {
			return nil, err
		}
		// A size is published only once R2 has confirmed it: a pending or missing
		// attachment reports none rather than repeating the client's claim.
		if a.State == AttachStored {
			confirmed := size
			a.ByteSize = &confirmed
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// patch is a triage change; nil fields are left untouched.
type patch struct {
	State        *string
	InternalNote *string
	NoteSet      bool
	Kind         *string
}

// Patch applies a triage change, stamping resolved_at on the two terminal states
// and clearing it when a report is reopened. It returns false when ref is unknown.
func (s *Store) Patch(ctx context.Context, ref string, p patch, now time.Time) (bool, error) {
	ts := timeutil.Format(now)
	sets := []string{"updated_at = ?"}
	args := []any{ts}
	if p.State != nil {
		sets = append(sets, "state = ?")
		args = append(args, *p.State)
		if terminalState(*p.State) {
			sets = append(sets, "resolved_at = ?")
			args = append(args, ts)
		} else {
			sets = append(sets, "resolved_at = NULL")
		}
	}
	if p.NoteSet {
		sets = append(sets, "internal_note = ?")
		args = append(args, p.InternalNote)
	}
	if p.Kind != nil {
		sets = append(sets, "kind = ?")
		args = append(args, *p.Kind)
	}
	args = append(args, ref)
	res, err := s.db.ExecContext(ctx, `UPDATE feedback_report SET `+strings.Join(sets, ", ")+` WHERE ref = ?`, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Delete removes a report and returns the object keys that belonged to it.
//
// ⚠ Order is normative (V3-D05): the keys are collected INSIDE the transaction
// and the R2 deletes are issued by the caller AFTER it commits. The reverse order
// destroys the attachments of a report that still exists if the commit fails.
func (s *Store) Delete(ctx context.Context, ref string) (keys []string, found bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM feedback_report WHERE ref = ?`, ref).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	keys, err = collectKeys(ctx, tx, `SELECT object_key FROM feedback_attachment WHERE report_id = ?`, id)
	if err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM feedback_report WHERE id = ?`, id); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return keys, true, nil
}

// Attachment returns one attachment's object key and content type for the
// view-URL route. It returns false for an unknown report, an unknown attachment,
// or one that is not `stored` — a pending or missing object has no URL to mint
// and must not produce a signed link to nothing.
func (s *Store) Attachment(ctx context.Context, ref string, attachmentID int64) (key, contentType string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT a.object_key, a.content_type FROM feedback_attachment a
		   JOIN feedback_report r ON r.id = a.report_id
		  WHERE r.ref = ? AND a.id = ? AND a.state = ?`, ref, attachmentID, AttachStored).
		Scan(&key, &contentType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return key, contentType, true, nil
}

// --- claim ------------------------------------------------------------------

// pendingObject is one attachment awaiting confirmation.
type pendingObject struct {
	ID        int64
	ObjectKey string
	Declared  int64
}

// PendingForRef returns the pending attachments of a report belonging to siteID,
// and whether that report exists at all. The rows are drained before the caller
// HEADs anything: those are network calls and must not run inside a cursor.
func (s *Store) PendingForRef(ctx context.Context, siteID, ref string) (objs []pendingObject, found bool, err error) {
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM feedback_report WHERE ref = ? AND site_id = ?`, ref, siteID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, object_key, byte_size FROM feedback_attachment
		  WHERE report_id = ? AND state = ? ORDER BY id ASC`, id, AttachPending)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var o pendingObject
		if err := rows.Scan(&o.ID, &o.ObjectKey, &o.Declared); err != nil {
			return nil, false, err
		}
		objs = append(objs, o)
	}
	return objs, true, rows.Err()
}

// SettleAttachment records a claim outcome: `stored` with the size R2 reported,
// or `missing`. It is scoped to one attachment id, so a claim can only ever touch
// the report that named it.
func (s *Store) SettleAttachment(ctx context.Context, id int64, state string, size int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE feedback_attachment SET state = ?, byte_size = ?, claimed_at = ? WHERE id = ?`,
		state, size, timeutil.Format(now), id)
	return err
}

// AttachmentsForRef returns the wire attachments of a report by ref (the claim
// response).
//
// A report that vanished between the claim's lookup and this read yields an
// EMPTY slice, never nil: openapi requires `attachments` to be a present array,
// and a widget iterating a null would throw into the host page — the one thing
// V3-D37 says it must never do.
func (s *Store) AttachmentsForRef(ctx context.Context, ref string) ([]AttachmentSummary, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM feedback_report WHERE ref = ?`, ref).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return []AttachmentSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.attachments(ctx, id)
}

// --- the sweep's queries ----------------------------------------------------

// UnclaimedBefore returns pending attachments created before cutoff — the ones
// the sweep marks missing and whose objects it deletes.
func (s *Store) UnclaimedBefore(ctx context.Context, cutoff time.Time) ([]pendingObject, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, object_key, byte_size FROM feedback_attachment
		  WHERE state = ? AND created_at < ? ORDER BY id ASC`, AttachPending, timeutil.Format(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pendingObject
	for rows.Next() {
		var o pendingObject
		if err := rows.Scan(&o.ID, &o.ObjectKey, &o.Declared); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// LiveObjectKeys returns every object key a live attachment row still claims —
// `pending` (an upload that may still be in flight) and `stored`. A `missing`
// row's key is deliberately NOT live: if such an object exists at all it is the
// truncated remains of a failed upload, and nothing will ever read it.
func (s *Store) LiveObjectKeys(ctx context.Context) (map[string]struct{}, error) {
	keys, err := collectKeys(ctx, s.db,
		`SELECT object_key FROM feedback_attachment WHERE state IN (?, ?)`, AttachPending, AttachStored)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return set, nil
}

// --- board counts and the site cascade --------------------------------------

// ReportCounts returns, per site id, the number of reports in state `new` — the
// unread badge on the board. It implements sites.ReportCounter, which is how the
// count crosses the module boundary without `sites` importing `feedback`.
func (s *Store) ReportCounts(ctx context.Context, siteIDs []string) (map[string]int, error) {
	if len(siteIDs) == 0 {
		return map[string]int{}, nil
	}
	args := make([]any, 0, len(siteIDs)+1)
	args = append(args, StateNew)
	for _, id := range siteIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT site_id, COUNT(*) FROM feedback_report
		  WHERE state = ? AND site_id IN (`+placeholders(len(siteIDs))+`) GROUP BY site_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := map[string]int{}
	for rows.Next() {
		var (
			id string
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		all[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Every requested site gets an entry: a site with no reports has a zero badge,
	// not an absent one. Absence means "nobody asked", which is decided by whether
	// this counter is registered at all (V3-D53).
	out := make(map[string]int, len(siteIDs))
	for _, id := range siteIDs {
		out[id] = all[id]
	}
	return out, nil
}

// SiteObjectKeys returns every object key belonging to a site, read inside tx so
// the set is exactly what the cascade about to run will orphan. It is the collect
// half of sites.ObjectPurger.
func (s *Store) SiteObjectKeys(ctx context.Context, tx *sql.Tx, siteID string) ([]string, error) {
	return collectKeys(ctx, tx,
		`SELECT a.object_key FROM feedback_attachment a
		   JOIN feedback_report r ON r.id = a.report_id
		  WHERE r.site_id = ?`, siteID)
}

// --- helpers ----------------------------------------------------------------

// placeholders renders n comma-separated "?" for an IN list. n is always a slice
// length the caller controls, never user input.
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// collectKeys drains a single-column key query, closing the cursor before it
// returns so the caller never holds the one connection while doing anything else.
func collectKeys(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// isUniqueViolation reports whether err is a UNIQUE constraint failure. The
// driver reports it in the message rather than as a typed error, and the only
// unique index a report insert can hit is feedback_report.ref.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

func nsToPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}
