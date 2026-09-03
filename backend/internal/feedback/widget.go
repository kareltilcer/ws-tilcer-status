package feedback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback/blob"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/ratelimit"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// maxMessageChars is the report text cap (openapi FeedbackSubmission.message).
const maxMessageChars = 4000

// Console tail caps. The widget applies them too and shows the reporter exactly
// what will be sent; these are the server's own bound, because the widget's is a
// promise made by code running on someone else's page.
const (
	maxConsoleLines    = 50
	maxConsoleLineRune = 200
)

// widgetConfig handles GET /api/ingest/{siteId}/feedback/config (public).
//
// The widget renders NOTHING until this answers, so a disabled site, an unknown
// key or a network failure means no launcher at all rather than a button that
// fails when pressed (V3-D35).
func (m *Module) widgetConfig(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteId")
	cfg, ok := m.authorize(w, r, siteID, allowDisabled)
	if !ok {
		return
	}
	if !m.limit(w, r, m.auxKeyLimiter, m.auxIPLimiter, cfg.WidgetKeyHash) {
		return
	}
	// A disabled site (or a deployment with no object storage) gets the bare
	// answer: enabled false, and no ticket to submit with.
	if !cfg.Enabled || !m.storageReady() {
		httpx.JSON(w, http.StatusOK, WidgetConfig{Enabled: false})
		return
	}

	now := time.Now().UTC()
	t := newTicket(siteID, now)
	if err := m.store.InsertTicket(r.Context(), t, now.Add(ticketTTL)); err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	token := signTicket(t, m.cfg.TicketSecret)
	httpx.JSON(w, http.StatusOK, WidgetConfig{
		Enabled:        true,
		Ticket:         &token,
		Kinds:          kinds,
		MaxFiles:       m.cfg.MaxFiles,
		MaxImageBytes:  m.cfg.MaxImageBytes,
		MaxVideoBytes:  m.cfg.MaxVideoBytes,
		MaxTextBytes:   m.cfg.MaxTextBytes,
		Accept:         acceptList(),
		ConsoleCapture: cfg.ConsoleCapture,
		StringsVersion: stringsVersion,
	})
}

// submit handles POST /api/ingest/{siteId}/feedback (public).
//
// The guard chain runs in a fixed order, enumerated by TestGuardChainOrder:
// 404 unknown site → 401 bad widget key → 403 disabled → 403 origin → 429 over
// rate → 413 body too large → 422 invalid → 202.
//
// ⚠ The body is never read before the key is checked — the v2 ingest principle,
// unchanged.
func (m *Module) submit(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteId")
	cfg, ok := m.authorize(w, r, siteID, requireEnabled)
	if !ok {
		return
	}
	if !m.limit(w, r, m.keyLimiter, m.ipLimiter, cfg.WidgetKeyHash) {
		return
	}

	// Body cap BEFORE decode (413), then a strict decode (422).
	r.Body = http.MaxBytesReader(w, r.Body, m.cfg.MaxTextBytes)
	var in Submission
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			// ⚠ The limit is named because it bounds the WHOLE body, not the message:
			// a 4 000-character message beside a long page_url, or an opted-in console
			// tail near its 50 × 200 bound, can reach it while every individual field
			// is within its own documented cap. A reporter who is only told "too
			// large" cannot tell which part to shorten, and STATUS_FEEDBACK_MAX_TEXT_BYTES
			// is the deployment's dial for it.
			httpx.WriteError(w, &httpx.APIError{
				Status: http.StatusRequestEntityTooLarge,
				Code:   "payload_too_large",
				Detail: fmt.Sprintf("report payload exceeds the %d-byte limit", m.cfg.MaxTextBytes),
			})
			return
		}
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	if dec.More() {
		httpx.WriteError(w, httpx.ErrUnprocessable("unexpected trailing content in request body"))
		return
	}

	now := time.Now().UTC()
	report, files, err := m.validateSubmission(r, in, cfg, siteID, now)
	if err != nil {
		// ⚠ One of validateSubmission's failures is not the caller's: the database
		// refusing to spend the ticket. Reporting that as a 422 tells a reporter
		// holding a perfectly valid ticket that it is invalid, and leaves their
		// dialog dead until a full reload mints another one.
		if errors.Is(err, errSubmissionInternal) {
			m.logger.Error("feedback submit", "site", siteID, "err", err)
			httpx.WriteError(w, httpx.ErrInternal(""))
			return
		}
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}

	ref, slots, err := m.store.InsertReport(r.Context(), report, files, now)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}

	// Presigning happens AFTER the insert transaction has committed: an R2 round
	// trip inside it would hold the service's only connection for the length of
	// someone else's TCP timeout (V3-D05a).
	//
	// ⚠ The slots are all-or-nothing. The contract promises "one slot per declared
	// file, in the order declared", and the widget pairs them with its own File
	// list by index — so handing back a short list would silently shift every file
	// after the gap onto the wrong slot. Signing is local computation over static
	// credentials, so a failure here means none of them can be signed; the report
	// is kept regardless (it is the thing worth keeping), its rows stay pending,
	// and the sweep resolves them.
	uploads := make([]UploadSlot, 0, len(slots))
	for _, s := range slots {
		up, err := m.blobs.PresignPut(r.Context(), s.ObjectKey, s.ContentType, s.ByteSize, m.cfg.UploadTTL)
		if err != nil {
			m.logger.Error("feedback presign upload", "site", siteID, "ref", ref, "err", err)
			uploads = uploads[:0]
			break
		}
		uploads = append(uploads, UploadSlot{
			AttachmentID: s.AttachmentID,
			URL:          up.URL,
			ExpiresAt:    timeutil.Format(up.ExpiresAt),
			Headers:      up.Headers,
		})
	}
	httpx.JSON(w, http.StatusAccepted, Accepted{Ref: ref, Uploads: uploads})
}

// claim handles POST /api/ingest/{siteId}/feedback/{ref}/claim (public).
//
// It HEADs every pending object and records the size R2 reports, not the size the
// client declared.
func (m *Module) claim(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteId")
	cfg, ok := m.authorize(w, r, siteID, requireEnabled)
	if !ok {
		return
	}
	// ⚠ The claim spends the auxiliary budget, NOT the reporting one. It is the
	// second half of a report that has already been accepted and paid for; making
	// each report cost two report tokens would refuse the claim of a reporter's
	// second report inside the refill window, and a claim that never lands loses
	// the attachment the reporter already uploaded (see auxRateFactor).
	if !m.limit(w, r, m.auxKeyLimiter, m.auxIPLimiter, cfg.WidgetKeyHash) {
		return
	}
	ref := chi.URLParam(r, "ref")
	if !ValidRef(ref) {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return
	}
	// Scoped to the named ref AND this site's key: a claim can never touch another
	// site's report.
	pending, found, err := m.store.PendingForRef(r.Context(), siteID, ref)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !found {
		httpx.WriteError(w, httpx.ErrNotFound("unknown report"))
		return
	}

	// The rows are drained before the first HEAD: these are network calls and must
	// not run with the one connection held by a cursor.
	now := time.Now().UTC()
	for _, p := range pending {
		size, err := m.blobs.Head(r.Context(), p.ObjectKey)
		switch {
		case errors.Is(err, blob.ErrNotFound):
			// Never uploaded, or the PUT was refused for a signature mismatch.
			if err := m.store.SettleAttachment(r.Context(), p.ID, AttachMissing, p.Declared, now); err != nil {
				httpx.WriteError(w, httpx.ErrInternal(""))
				return
			}
		case err != nil:
			// A transport failure is not evidence of absence; leave the row pending
			// so a later claim, or the sweep, decides.
			m.logger.Error("feedback claim head", "site", siteID, "ref", ref, "key", p.ObjectKey, "err", err)
		case size != p.Declared:
			// Smaller than declared: the client stalled mid-body.
			if err := m.store.SettleAttachment(r.Context(), p.ID, AttachMissing, size, now); err != nil {
				httpx.WriteError(w, httpx.ErrInternal(""))
				return
			}
		default:
			// ⚠ Presence and cap, NOT integrity. R2 truncates a lying upload to the
			// signed length and answers 200 (V3-D54, probe 3), so an object matching
			// its declared size may be the first 1 KiB of a 64 KiB file. Do not
			// "improve" this into a guarantee it cannot make — that would need a
			// signed checksum, which was considered and declined in FR-18.
			if err := m.store.SettleAttachment(r.Context(), p.ID, AttachStored, size, now); err != nil {
				httpx.WriteError(w, httpx.ErrInternal(""))
				return
			}
		}
	}

	out, err := m.store.AttachmentsForRef(r.Context(), ref)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	httpx.JSON(w, http.StatusOK, ClaimResult{Attachments: out})
}

// --- the guard chain --------------------------------------------------------

// disabledPolicy says what a disabled site means for a route: the config route
// answers it (enabled: false), the two writing routes refuse it.
type disabledPolicy bool

const (
	allowDisabled  disabledPolicy = true
	requireEnabled disabledPolicy = false
)

// authorize runs the first four guards — 404 unknown site, 401 bad widget key,
// 403 disabled, 403 foreign origin — writing the response itself and returning
// ok=false when it did.
//
// ⚠ Nothing here reads the request body. That is the point of the ordering: an
// untrusted body is never parsed on a bad key.
func (m *Module) authorize(w http.ResponseWriter, r *http.Request, siteID string, policy disabledPolicy) (*siteConfig, bool) {
	if !sites.ValidID(siteID) {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return nil, false
	}
	// A configuration row implies the site exists (the FK cascades from it), so the
	// existence check only runs when there is no row — the miss, not the hot path.
	cfg, err := m.store.Config(r.Context(), siteID)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return nil, false
	}
	if cfg == nil {
		exists, err := m.sitesStore.Exists(r.Context(), siteID)
		if err != nil {
			httpx.WriteError(w, httpx.ErrInternal(""))
			return nil, false
		}
		if !exists {
			httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
			return nil, false
		}
		// The site is real but has no widget key, so no key can be valid. It is a
		// 401 rather than a 403: nothing has proved it holds a key yet.
		httpx.WriteError(w, httpx.ErrUnauthorized("invalid widget key"))
		return nil, false
	}

	key := r.Header.Get("X-Widget-Key")
	if key == "" || !sites.ConstantTimeMatch(key, cfg.WidgetKeyHash) {
		httpx.WriteError(w, httpx.ErrUnauthorized("invalid widget key"))
		return nil, false
	}

	// Disabled-and-known is a 403, not a 404: the caller has already proved it
	// holds the key, so there is nothing left to conceal — and a 404 would send
	// Karel debugging a site id that is correct.
	if policy == requireEnabled && (!cfg.Enabled || !m.storageReady()) {
		httpx.WriteError(w, httpx.ErrForbidden("feedback is disabled for this site"))
		return nil, false
	}

	// Origin: only judged when the request carries one. A caller with no Origin is
	// not a browser, and this check has never claimed to stop a shell prompt — it
	// stops another *page* using a lifted key.
	if origin := r.Header.Get("Origin"); origin != "" && !httpx.MatchOrigin(origin, m.cfg.AllowedOrigins...) {
		httpx.WriteError(w, httpx.ErrForbidden("origin not allowed"))
		return nil, false
	}
	return cfg, true
}

// limit applies the per-key and per-IP token buckets, answering 429 with
// Retry-After. The client IP comes from the request metadata the router already
// resolved under STATUS_TRUSTED_PROXY_COUNT — never a second XFF parser (V3-D23).
func (m *Module) limit(w http.ResponseWriter, r *http.Request, keyLimiter, ipLimiter *ratelimit.Limiter, keyHash string) bool {
	if ok, retry := keyLimiter.Allow(keyHash); !ok {
		writeRateLimited(w, retry)
		return false
	}
	if ip := reqctx.IP(r.Context()); ip != "" {
		if ok, retry := ipLimiter.Allow(ip); !ok {
			writeRateLimited(w, retry)
			return false
		}
	}
	return true
}

// writeRateLimited answers 429 with Retry-After. The wording is deliberately
// about requests rather than reports: this is shared by the config fetch and the
// claim, which spend the auxiliary budget precisely because they are not reports,
// and telling a reporter who has filed none that they filed too many sends
// whoever reads it looking for a flood that never happened.
func writeRateLimited(w http.ResponseWriter, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
	httpx.WriteError(w, &httpx.APIError{
		Status: http.StatusTooManyRequests,
		Code:   "rate_limited",
		Detail: "too many requests, slow down",
	})
}

// --- validation -------------------------------------------------------------

// errSubmissionInternal marks the one validateSubmission failure that is NOT the
// caller's fault — the database refusing to spend an otherwise valid ticket. It
// is a 500, not a 422: every other failure here is something the reporter can
// see and fix, and telling them a valid ticket is invalid costs them the only
// one their page holds.
var errSubmissionInternal = errors.New("feedback: could not spend the ticket")

// validateSubmission turns a decoded body into a report ready to insert, or the
// single 422 that covers every way a submission can be invalid.
func (m *Module) validateSubmission(r *http.Request, in Submission, cfg *siteConfig, siteID string, now time.Time) (newReport, []resolvedFile, error) {
	// Honeypot first: it costs nothing and a filled one means nothing else matters.
	if in.Website != nil && strings.TrimSpace(*in.Website) != "" {
		return newReport{}, nil, errors.New("invalid submission")
	}

	t, err := parseTicket(in.Ticket, m.cfg.TicketSecret)
	if err != nil {
		return newReport{}, nil, errors.New("invalid or expired ticket")
	}
	if t.SiteID != siteID {
		return newReport{}, nil, errors.New("invalid or expired ticket")
	}
	if err := t.checkTiming(now, m.cfg.MinDwell); err != nil {
		return newReport{}, nil, errors.New("invalid or expired ticket")
	}
	msg := strings.TrimSpace(in.Message)
	if msg == "" {
		return newReport{}, nil, errors.New("message is required")
	}
	if len([]rune(msg)) > maxMessageChars {
		return newReport{}, nil, errors.New("message exceeds 4000 characters")
	}
	kind := in.Kind
	if kind == "" {
		kind = KindBug
	}
	if !validKind(kind) {
		return newReport{}, nil, errors.New("kind must be one of bug|idea|other")
	}
	files, err := m.resolveFiles(in.Files)
	if err != nil {
		return newReport{}, nil, err
	}

	// The ticket is spent LAST, once the payload is known to be acceptable. Its
	// signature, site and timing were all checked above, so nothing unverified has
	// reached the database — and a 422 the reporter can fix (an unsupported file
	// type, an over-long message) no longer costs them the one ticket their page
	// holds, which would leave the dialog dead until a full reload.
	//
	// Single use is a property of the database: the row is deleted, and a second
	// attempt finds nothing to delete.
	spent, err := m.store.SpendTicket(r.Context(), t.ID, siteID)
	if err != nil {
		return newReport{}, nil, fmt.Errorf("%w: %v", errSubmissionInternal, err)
	}
	if !spent {
		return newReport{}, nil, errors.New("invalid or expired ticket")
	}

	report := newReport{
		SiteID:        siteID,
		Kind:          kind,
		Message:       msg,
		ReporterLabel: trimPtr(in.ReporterLabel, 200),
		PageURL:       trimPtr(in.PageURL, 2000),
		Referrer:      trimPtr(in.Referrer, 2000),
		UserAgent:     trimPtr(strPtr(r.UserAgent()), 500),
		Viewport:      trimPtr(in.Viewport, 40),
		Locale:        trimPtr(in.Locale, 40),
		AppRelease:    trimPtr(in.AppRelease, 100),
		IPHash:        m.hashIP(reqctx.IP(r.Context())),
	}
	// The console tail is stored only when the site opted in. ⚠ A tail from a host
	// app with a private-item model could carry a private title into an admin
	// inbox, so an un-opted site's lines are dropped here rather than trusted to
	// have never been sent (PRD §V3-8).
	if cfg.ConsoleCapture {
		report.ConsoleTail = capConsole(in.ConsoleTail)
		report.LastError = trimPtr(in.LastError, maxConsoleLineRune)
	}
	return report, files, nil
}

// hashIP returns SHA-256(ip + salt). ⚠ The IP itself never reaches a row; only
// this digest does, and it is useless without the deployment's salt.
func (m *Module) hashIP(ip string) *string {
	if ip == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(ip + m.cfg.IPHashSalt))
	h := hex.EncodeToString(sum[:])
	return &h
}

// capConsole enforces the 50 × 200 console-tail bound.
func capConsole(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) > maxConsoleLines {
		lines = lines[:maxConsoleLines]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, truncateRunes(l, maxConsoleLineRune))
	}
	return out
}

// trimPtr trims and caps an optional string, returning nil when it is empty.
func trimPtr(s *string, max int) *string {
	if s == nil {
		return nil
	}
	v := truncateRunes(strings.TrimSpace(*s), max)
	if v == "" {
		return nil
	}
	return &v
}

func strPtr(s string) *string { return &s }

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
