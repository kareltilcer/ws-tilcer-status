// Package crash is the public crash-ingest + grouping module. It accepts events
// on a per-site ingest key, groups them by fingerprint, and serves admin browse
// and triage. It owns no tables (the schema lives in the sites registry); it
// reads the site's ingest key hash and recomputes color through sites.
package crash

import (
	"database/sql"
	"io/fs"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/ratelimit"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// Module implements registry.Module (gated browse/triage) plus a public ingest
// surface mounted outside the session gate.
type Module struct {
	db         *sql.DB
	crashStore *Store
	sitesStore *sites.Store
	limiter    *ratelimit.Limiter
	cfg        Config
	// notifier is told about every accepted event (SetNotifier); nil tells nobody.
	notifier Notifier
}

// NewModule builds the crash module. sitesStore provides the per-site ingest key
// lookup and the color recompute helper.
func NewModule(db *sql.DB, sitesStore *sites.Store, cfg Config) *Module {
	return &Module{
		db:         db,
		crashStore: NewStore(db),
		sitesStore: sitesStore,
		limiter:    ratelimit.New(cfg.IngestRatePerSec, cfg.IngestBurst, nil),
		cfg:        cfg,
	}
}

func (m *Module) Name() string { return "crash" }

// Migrations returns nil — the crash tables live in the sites registry migration.
func (m *Module) Migrations() fs.FS { return nil }

// RegisterRoutes mounts the gated admin browse/triage endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Get("/sites/{id}/crashes", m.listCrashes)
	r.Get("/crashes/{groupId}", m.getGroup)
	r.With(httpx.RequireAdmin).Patch("/crashes/{groupId}", m.triage)
}

// RegisterPublicRoutes mounts the public, key-authenticated ingest endpoint. It
// is mounted OUTSIDE the session gate (via httpx.Deps.MountPublicAPI).
func (m *Module) RegisterPublicRoutes(api chi.Router) {
	api.Post("/ingest/{siteId}", m.ingest)
}
