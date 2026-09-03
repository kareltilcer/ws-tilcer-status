// Package sites is the shared registry every module keys off: a user-supplied
// site id is defined once here and used by both monitoring and crash. It owns the
// whole database schema (one initial migration), CRUD, ingest-key lifecycle, and
// the FR-6 color computation.
package sites

import (
	"database/sql"
	"io/fs"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
)

// Module implements registry.Module for the sites registry.
type Module struct {
	store *Store
	// objects is the injected object-storage half of the site cascade. Nil until
	// composition registers the feedback module, and nil in a deployment without
	// it — the SQL cascade then has nothing to accompany.
	objects ObjectPurger
}

// NewModule builds the sites module. redThreshold (FR-6) is used to compute color
// on read.
func NewModule(db *sql.DB, redThreshold int) *Module {
	return &Module{store: NewStore(db, redThreshold)}
}

// Store exposes the registry store so the crash and monitoring modules can reuse
// the ingest-key lookup and color helpers without a second connection.
func (m *Module) Store() *Store { return m.store }

func (m *Module) Name() string { return "sites" }

func (m *Module) Migrations() fs.FS { return MigrationsFS }

// RegisterRoutes mounts the admin site endpoints on the gated /api group. Reads
// require an authenticated session (the session middleware); mutations require
// the admin role.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Get("/sites", m.listSites)
	r.With(httpx.RequireAdmin).Post("/sites", m.createSite)
	r.Get("/sites/{id}", m.getSite)
	r.With(httpx.RequireAdmin).Patch("/sites/{id}", m.patchSite)
	r.With(httpx.RequireAdmin).Delete("/sites/{id}", m.deleteSite)
	r.With(httpx.RequireAdmin).Post("/sites/{id}/rotate-key", m.rotateKey)
}
