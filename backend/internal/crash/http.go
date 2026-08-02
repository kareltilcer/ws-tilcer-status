package crash

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// listCrashes handles GET /api/sites/{id}/crashes.
func (m *Module) listCrashes(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	exists, err := m.sitesStore.Exists(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !exists {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	q := r.URL.Query()
	statusFilter := q.Get("status")
	if statusFilter != "" && !validStatus(statusFilter) {
		httpx.WriteError(w, httpx.ErrUnprocessable("status must be one of open|resolved|ignored"))
		return
	}
	levelFilter := q.Get("level")
	if levelFilter != "" && !validLevel(levelFilter) {
		httpx.WriteError(w, httpx.ErrUnprocessable("level must be one of fatal|error|warning"))
		return
	}
	page, err := m.crashStore.ListGroups(r.Context(), id, statusFilter, levelFilter, httpx.AtoiDefault(q.Get("limit"), 0), q.Get("cursor"))
	if errors.Is(err, errInvalidCursor) {
		httpx.WriteError(w, httpx.ErrUnprocessable("invalid cursor"))
		return
	}
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

// getGroup handles GET /api/crashes/{groupId}.
func (m *Module) getGroup(w http.ResponseWriter, r *http.Request) {
	gid, ok := parseGroupID(r)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound("unknown crash group"))
		return
	}
	g, err := m.crashStore.GetGroup(r.Context(), gid)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if g == nil {
		httpx.WriteError(w, httpx.ErrNotFound("unknown crash group"))
		return
	}
	q := r.URL.Query()
	events, next, err := m.crashStore.ListEvents(r.Context(), gid, httpx.AtoiDefault(q.Get("limit"), 0), q.Get("cursor"))
	if errors.Is(err, errInvalidCursor) {
		httpx.WriteError(w, httpx.ErrUnprocessable("invalid cursor"))
		return
	}
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	httpx.JSON(w, http.StatusOK, GroupDetail{Group: *g, Events: events, NextCursor: next})
}

type triageReq struct {
	Status string `json:"status"`
}

// triage handles PATCH /api/crashes/{groupId}. Changing status recomputes the
// site's color (resolving/ignoring removes the group from the orange signal).
func (m *Module) triage(w http.ResponseWriter, r *http.Request) {
	gid, ok := parseGroupID(r)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound("unknown crash group"))
		return
	}
	var in triageReq
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	if !validStatus(in.Status) {
		httpx.WriteError(w, httpx.ErrUnprocessable("status must be one of open|resolved|ignored"))
		return
	}
	now := time.Now().UTC()
	var found bool
	if err := appdb.WithTx(r.Context(), m.db, func(tx *sql.Tx) error {
		siteID, ok, err := m.crashStore.SetGroupStatus(r.Context(), tx, gid, in.Status)
		if err != nil {
			return err
		}
		found = ok
		if !ok {
			return nil
		}
		_, err = sites.RecomputeAndPersist(r.Context(), tx, siteID, m.cfg.RedFailThreshold, now)
		return err
	}); err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !found {
		httpx.WriteError(w, httpx.ErrNotFound("unknown crash group"))
		return
	}
	g, err := m.crashStore.GetGroup(r.Context(), gid)
	if err != nil || g == nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	httpx.JSON(w, http.StatusOK, g)
}

func parseGroupID(r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "groupId")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
