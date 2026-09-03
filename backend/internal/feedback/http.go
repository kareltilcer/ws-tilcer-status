package feedback

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// fail answers the module's opaque 500 and records what actually went wrong.
//
// ⚠ The response body stays empty on purpose — a database error is not the
// caller's business — but until this existed most of those 500s were written
// with no log line at all, so an operator seeing one had nothing anywhere to
// look at. `op` names the step, not the error: it is what turns "something in
// feedback broke" into "the inbox query broke".
func (m *Module) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	info, _ := reqctx.RequestFrom(r.Context())
	m.logger.Error("feedback "+op, "err", err, "request_id", info.RequestID, "path", r.URL.Path)
	httpx.WriteError(w, httpx.ErrInternal(""))
}

// listReports handles GET /api/reports — the cross-site inbox.
func (m *Module) listReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	if state != "" && !validState(state) {
		httpx.WriteError(w, httpx.ErrUnprocessable("state must be one of new|open|resolved|declined"))
		return
	}
	kind := q.Get("kind")
	if kind != "" && !validKind(kind) {
		httpx.WriteError(w, httpx.ErrUnprocessable("kind must be one of bug|idea|other"))
		return
	}
	// The site filter is validated like the other two: a typo that silently
	// returns an empty page reads to the operator as "this site has no reports".
	site := q.Get("site")
	if site != "" && !sites.ValidID(site) {
		httpx.WriteError(w, httpx.ErrUnprocessable("site must be a valid site id"))
		return
	}
	page, err := m.store.ListReports(r.Context(), state, site, kind,
		httpx.AtoiDefault(q.Get("limit"), 0), q.Get("cursor"))
	if errors.Is(err, errInvalidCursor) {
		httpx.WriteError(w, httpx.ErrUnprocessable("invalid cursor"))
		return
	}
	if err != nil {
		m.fail(w, r, "list reports", err)
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

// getReport handles GET /api/reports/{ref}.
//
// ⚠ The internal note is stripped for a caller without the admin role.
// `openapi.yaml` calls that field "Admin-only" and FR-21 says the same, but the
// route itself is only session-gated — so the field, not the route, is what the
// gate has to be about. Gating the whole route would be the larger change and
// the wrong one: a non-admin session can already list reports, and there is no
// reason it should be unable to read one.
func (m *Module) getReport(w http.ResponseWriter, r *http.Request) {
	rep, ok := m.loadReport(w, r)
	if !ok {
		return
	}
	if !httpx.IsAdmin(r.Context()) {
		rep.InternalNote = nil
	}
	httpx.JSON(w, http.StatusOK, rep)
}

type reportPatchReq struct {
	State *string `json:"state"`
	// InternalNote is raw so that an ABSENT field and an explicit null are
	// distinguishable: openapi types it [string, "null"], and null is how the
	// dashboard clears a note. A *string collapses the two into nil.
	InternalNote json.RawMessage `json:"internal_note"`
	Kind         *string         `json:"kind"`
}

// jsonNull is the literal an explicit null decodes to.
var jsonNull = []byte("null")

// errReportVanished names the read-back below finding nothing, so that the 500
// it produces reaches the log as something rather than as `err=<nil>`. It is the
// one way into `fail` that has no error of its own, and an ERROR line with no
// error in it is the state `fail` was written to end.
var errReportVanished = errors.New("feedback: the patched report could not be read back")

// patchReport handles PATCH /api/reports/{ref} (admin): state, internal note,
// kind.
//
// ⚠ Triage does not touch colour. A report is counted on the board, never
// coloured into it — see TestComputeColorUntouchedByFeedback.
func (m *Module) patchReport(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	if !ValidRef(ref) {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return
	}
	var in reportPatchReq
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	if in.State == nil && in.Kind == nil && in.InternalNote == nil {
		httpx.WriteError(w, httpx.ErrUnprocessable("at least one of state, internal_note or kind is required"))
		return
	}
	if in.State != nil && !validState(*in.State) {
		httpx.WriteError(w, httpx.ErrUnprocessable("state must be one of new|open|resolved|declined"))
		return
	}
	if in.Kind != nil && !validKind(*in.Kind) {
		httpx.WriteError(w, httpx.ErrUnprocessable("kind must be one of bug|idea|other"))
		return
	}
	p := patch{State: in.State, Kind: in.Kind}
	if in.InternalNote != nil {
		// Present at all means the note is being set: an explicit null (or an empty
		// string) clears it, anything else replaces it.
		p.NoteSet = true
		if !bytes.Equal(in.InternalNote, jsonNull) {
			var note string
			if err := json.Unmarshal(in.InternalNote, &note); err != nil {
				httpx.WriteError(w, httpx.ErrUnprocessable("internal_note must be a string or null"))
				return
			}
			p.InternalNote = trimPtr(&note, 4000)
		}
	}
	found, err := m.store.Patch(r.Context(), ref, p, time.Now().UTC())
	if err != nil {
		m.fail(w, r, "patch report", err)
		return
	}
	if !found {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return
	}
	rep, err := m.store.Get(r.Context(), ref)
	if err == nil && rep == nil {
		err = errReportVanished
	}
	if err != nil {
		// A report that patched a moment ago and cannot be read back now is a
		// database problem, not a 404: reporting it as "unknown report" would
		// send the reader looking for a row that is still there.
		m.fail(w, r, "reload patched report", err)
		return
	}
	httpx.JSON(w, http.StatusOK, rep)
}

// deleteReport handles DELETE /api/reports/{ref} (admin).
//
// ⚠ Order is normative (V3-D05): the keys are read inside the deleting
// transaction, the transaction commits, and only then are the objects removed. A
// delete that fails leaves an orphan for the nightly sweep; the response does not
// wait on R2.
func (m *Module) deleteReport(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	if !ValidRef(ref) {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return
	}
	keys, found, err := m.store.Delete(r.Context(), ref)
	if err != nil {
		m.fail(w, r, "delete report", err)
		return
	}
	if !found {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return
	}
	m.DeleteObjects(r.Context(), keys)
	w.WriteHeader(http.StatusNoContent)
}

// attachmentURL handles GET /api/reports/{ref}/attachments/{attachmentId}/url.
//
// ⚠ The URL is a bearer token for its lifetime: anyone it is forwarded to can
// open it until it expires. It is minted for VIEW_TTL (default 5 minutes) and the
// dashboard never places one in a shareable link.
func (m *Module) attachmentURL(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	id, err := strconv.ParseInt(chi.URLParam(r, "attachmentId"), 10, 64)
	if !ValidRef(ref) || err != nil || id <= 0 {
		httpx.WriteError(w, httpx.ErrNotFound("unknown attachment"))
		return
	}
	if !m.storageReady() {
		httpx.WriteError(w, httpx.ErrNotFound("unknown attachment"))
		return
	}
	// An unknown report, an unknown attachment, or one that is not `stored` all
	// answer 404: a pending or missing object has no URL to mint, and a signed
	// link to nothing is worse than a refusal.
	key, contentType, ok, err := m.store.Attachment(r.Context(), ref, id)
	if err != nil {
		m.fail(w, r, "load attachment", err)
		return
	}
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound("unknown attachment"))
		return
	}
	url, expires, err := m.blobs.PresignGet(r.Context(), key, m.cfg.ViewTTL)
	if err != nil {
		m.fail(w, r, "presign attachment view", err)
		return
	}
	httpx.JSON(w, http.StatusOK, AttachmentURL{
		URL:         url,
		ExpiresAt:   timeutil.Format(expires),
		ContentType: contentType,
	})
}

// getSiteConfig handles GET /api/sites/{id}/feedback-config. A site with no row
// reads as disabled rather than 404, so the dashboard renders the switch without
// a special case.
func (m *Module) getSiteConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	exists, err := m.sitesStore.Exists(r.Context(), id)
	if err != nil {
		m.fail(w, r, "look up site", err)
		return
	}
	if !exists {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	cfg, err := m.store.Config(r.Context(), id)
	if err != nil {
		m.fail(w, r, "read feedback config", err)
		return
	}
	httpx.JSON(w, http.StatusOK, wireConfig(id, cfg))
}

type configPatchReq struct {
	Enabled        *bool `json:"enabled"`
	ConsoleCapture *bool `json:"console_capture"`
}

// patchSiteConfig handles PATCH /api/sites/{id}/feedback-config (admin). On the
// first enable it mints the widget key and returns the plaintext exactly once —
// the `ik_` precedent.
func (m *Module) patchSiteConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	exists, err := m.sitesStore.Exists(r.Context(), id)
	if err != nil {
		m.fail(w, r, "look up site", err)
		return
	}
	if !exists {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	var in configPatchReq
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	if in.Enabled == nil && in.ConsoleCapture == nil {
		httpx.WriteError(w, httpx.ErrUnprocessable("at least one of enabled or console_capture is required"))
		return
	}
	// ⚠ The switch must not be flippable into a state the process cannot serve:
	// with no object storage configured there is no way to mint an upload URL, so
	// turning feedback on is a 503 rather than a setting that silently does nothing.
	if in.Enabled != nil && *in.Enabled && !m.storageReady() {
		httpx.WriteError(w, &httpx.APIError{
			Status: http.StatusServiceUnavailable,
			Code:   "storage_unconfigured",
			Detail: "this deployment has no object storage configured (STATUS_FEEDBACK_ENABLED)",
		})
		return
	}

	// ⚠ Only a request that turns feedback ON may create the configuration row,
	// and `mintKey == nil` is how that is said. Creating it mints the widget key
	// and returns the plaintext exactly once, so a `console_capture`-only PATCH
	// against a site that has never been enabled would otherwise burn that single
	// display on a setting the caller did not ask about — leaving a site whose
	// key nobody knows, recoverable only by rotating it.
	//
	// ⚠ A minter rather than a key, so that the row's absence — which is only
	// known inside the transaction that writes it — is also what decides whether
	// a secret is generated at all. Handing in a pre-minted key made every enable
	// of an already-configured site generate one and throw it away. An existing
	// key is never replaced by this route either way; that is rotate's job.
	var mintKey func() (string, string, error)
	if in.Enabled != nil && *in.Enabled {
		mintKey = GenerateWidgetKey
	}
	cfg, issuedKey, err := m.store.UpsertConfig(r.Context(), id, in.Enabled, in.ConsoleCapture, mintKey, time.Now().UTC())
	if errors.Is(err, errConfigMissing) {
		httpx.WriteError(w, httpx.ErrUnprocessable(
			"feedback is not configured for this site — enable it first, which is what issues its widget key"))
		return
	}
	if err != nil {
		m.fail(w, r, "upsert feedback config", err)
		return
	}
	// Shown once and never again: only the SHA-256 is stored. Non-empty on
	// exactly the request that created the row.
	out := FeedbackConfigWithKey{FeedbackConfig: wireConfig(id, cfg), WidgetKey: issuedKey}
	httpx.JSON(w, http.StatusOK, out)
}

// rotateWidgetKey handles POST /api/sites/{id}/rotate-widget-key (admin). The old
// key dies immediately.
//
// ⚠ It never touches the ingest key: a spammed widget can be revoked without
// silencing that site's crash reporting.
func (m *Module) rotateWidgetKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	exists, err := m.sitesStore.Exists(r.Context(), id)
	if err != nil {
		m.fail(w, r, "look up site", err)
		return
	}
	if !exists {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	plaintext, hash, err := GenerateWidgetKey()
	if err != nil {
		m.fail(w, r, "generate widget key", err)
		return
	}
	if err := m.store.SetWidgetKeyHash(r.Context(), id, hash, time.Now().UTC()); err != nil {
		m.fail(w, r, "rotate widget key", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"widget_key": plaintext})
}

// loadReport resolves {ref} to a report, writing the 404 itself.
func (m *Module) loadReport(w http.ResponseWriter, r *http.Request) (*Report, bool) {
	ref := chi.URLParam(r, "ref")
	if !ValidRef(ref) {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return nil, false
	}
	rep, err := m.store.Get(r.Context(), ref)
	if err != nil {
		m.fail(w, r, "load report", err)
		return nil, false
	}
	if rep == nil {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return nil, false
	}
	return rep, true
}

// wireConfig renders a stored configuration, or the absent-means-off shape when
// the site has no row.
func wireConfig(siteID string, c *siteConfig) FeedbackConfig {
	if c == nil {
		return FeedbackConfig{SiteID: siteID}
	}
	setAt, updated := c.WidgetKeySetAt, c.UpdatedAt
	return FeedbackConfig{
		SiteID:         c.SiteID,
		Enabled:        c.Enabled,
		ConsoleCapture: c.ConsoleCapture,
		WidgetKeySetAt: &setAt,
		UpdatedAt:      &updated,
	}
}

// --- the sites boundary -----------------------------------------------------

// ReportCounts implements sites.ReportCounter: the board's unread badge, counted
// here because `sites` may not import `feedback`. Composition injects it
// (cmd/status), never a package-level global.
func (m *Module) ReportCounts(ctx context.Context, siteIDs []string) (map[string]int, error) {
	return m.store.ReportCounts(ctx, siteIDs)
}

// SiteObjectKeys implements the collect half of sites.ObjectPurger: the object
// keys of a site's reports, read INSIDE the transaction that is about to cascade
// them away.
func (m *Module) SiteObjectKeys(ctx context.Context, tx *sql.Tx, siteID string) ([]string, error) {
	return m.store.SiteObjectKeys(ctx, tx, siteID)
}

// DeleteObjects removes objects from storage, AFTER the transaction that removed
// their rows has committed. A failure is logged and left to the sweep: an orphan
// is a cost, whereas deleting the objects of a report that still exists is data
// loss.
//
// ⚠ It does not block the caller (FR-22: "the response does not wait on R2"). The
// rows are already gone and committed, so an unreachable bucket must not hold a
// DELETE open for the length of someone else's TCP timeout; the sweep is the
// backstop for whatever these calls fail to remove. The context is detached from
// the request precisely because the request is about to end, and Drain lets a
// graceful shutdown finish what is in flight rather than turning every pending
// delete into an orphan.
//
// ⚠ It must never be called inside a transaction or with a cursor open — see
// blob's package comment for why (V3-D05a).
func (m *Module) DeleteObjects(ctx context.Context, keys []string) {
	if !m.storageReady() || len(keys) == 0 {
		return
	}
	// ⚠ Detached from the request, but NOT open-ended. The AWS client sets no
	// overall deadline of its own, so a bucket that accepts the connection and
	// never answers would leave this goroutine running forever — and Drain, which
	// a graceful shutdown blocks on, waiting with it until the container is
	// killed. A delete is best-effort by design and the sweep is its backstop, so
	// giving up after deleteBatchTimeout costs an orphan the sweep already knows
	// how to collect, whereas hanging costs the shutdown.
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), deleteBatchTimeout)
	m.deletes.Add(1)
	go func() {
		defer cancel()
		defer m.deletes.Done()
		for _, k := range keys {
			if err := m.blobs.Delete(detached, k); err != nil {
				m.logger.Error("feedback delete object", "key", k, "err", err)
			}
		}
	}()
}

// Drain waits for the object deletes DeleteObjects has in flight. Composition
// defers it so a shutdown lands them rather than leaving orphans for the sweep,
// and tests use it to observe a delete that the response deliberately does not
// wait for. It is bounded because every batch it waits on is (deleteBatchTimeout).
func (m *Module) Drain() { m.deletes.Wait() }
