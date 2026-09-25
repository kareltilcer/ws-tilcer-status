package crash

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// ingest handles POST /api/ingest/{siteId} (public, per-site key). The guard
// chain runs in a fixed order so an untrusted body is never parsed on a bad key:
// 404 unknown site → 401 bad key → 429 over-rate → 413 oversized → 422 invalid →
// 202 accepted.
func (m *Module) ingest(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteId")
	if !sites.ValidID(siteID) {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}

	// 1) resolve the site's stored key hash (404 for unknown — do not leak).
	hash, found, err := m.sitesStore.IngestKeyHash(r.Context(), siteID)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !found {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}

	// 2) constant-time key check (401).
	key := r.Header.Get("X-Ingest-Key")
	if key == "" || !sites.ConstantTimeMatch(key, hash) {
		httpx.WriteError(w, httpx.ErrUnauthorized("invalid ingest key"))
		return
	}

	// 3) per-site token-bucket rate limit (429 + Retry-After).
	if ok, retry := m.limiter.Allow(siteID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		httpx.WriteError(w, &httpx.APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Detail: "too many events, slow down"})
		return
	}

	// 4) body cap BEFORE decode (413), then strict decode (422).
	r.Body = http.MaxBytesReader(w, r.Body, m.cfg.MaxIngestBytes)
	var in CrashReport
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.WriteError(w, &httpx.APIError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Detail: "crash payload exceeds the size limit"})
			return
		}
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}

	// 5) validate.
	msg := strings.TrimSpace(in.Message)
	if msg == "" {
		httpx.WriteError(w, httpx.ErrUnprocessable("message is required"))
		return
	}
	level := in.Level
	if level == "" {
		level = LevelError
	}
	if !validLevel(level) {
		httpx.WriteError(w, httpx.ErrUnprocessable("level must be one of fatal|error|warning"))
		return
	}
	now := time.Now().UTC()
	occurredAt := now
	if in.OccurredAt != "" {
		t, err := timeutil.Parse(in.OccurredAt)
		if err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable("occurred_at must be an RFC3339 timestamp"))
			return
		}
		occurredAt = t
		if occurredAt.After(now) {
			// A client-supplied future timestamp would keep the site's crash window
			// (and last_seen) permanently ahead of the clock, so the orange status
			// could never age out by time. Clamp it to receipt time.
			occurredAt = now
		}
	}
	occStr := timeutil.Format(occurredAt)
	recvStr := timeutil.Format(now)

	var contextJSON string
	if len(in.Context) > 0 {
		b, err := json.Marshal(in.Context)
		if err != nil {
			// in.Context is decoded from the request JSON, so this is virtually
			// unreachable — but surface it as a 422 rather than silently storing the
			// event without its context.
			httpx.WriteError(w, httpx.ErrUnprocessable("context is not serializable"))
			return
		}
		contextJSON = string(b)
	}

	// 6) one tx: fingerprint → group upsert → event insert → color recompute →
	// notifier. ⚠ The notifier is the LAST statement: it runs inside a savepoint
	// of its own, and a transaction SQLite has already rolled back must not be
	// followed by a statement of ours that would then commit on its own.
	fp := Fingerprint(siteID, level, in.Message, in.Stack, in.Fingerprint)
	title := truncate(msg, 200)

	var groupID, eventID int64
	if err := appdb.WithTx(r.Context(), m.db, func(tx *sql.Tx) error {
		g, err := m.crashStore.UpsertGroup(r.Context(), tx, siteID, fp, title, level, occStr, m.cfg.ReopenOnRegression)
		if err != nil {
			return err
		}
		eid, err := m.crashStore.InsertEvent(r.Context(), tx, siteID, g.ID, level, in.Message,
			in.Stack, in.Environment, in.Release, contextJSON, occStr, recvStr)
		if err != nil {
			return err
		}
		if _, err := sites.RecomputeAndPersist(r.Context(), tx, siteID, m.cfg.RedFailThreshold, now); err != nil {
			return err
		}
		groupID, eventID = g.ID, eid
		if m.notifier == nil {
			return nil
		}
		return m.notifier.CrashRecorded(r.Context(), tx, Signal{
			SiteID: siteID, GroupID: g.ID, EventID: eid,
			Created: g.Created, Reopened: g.Reopened, GroupStatus: g.Status,
			Level: level, Environment: in.Environment, Release: in.Release,
			Title: title, Message: msg, At: now,
		})
	}); err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	httpx.JSON(w, http.StatusAccepted, ingestResp{GroupID: groupID, EventID: eventID})
}

// truncate shortens s to at most n runes (title from the first event message).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
