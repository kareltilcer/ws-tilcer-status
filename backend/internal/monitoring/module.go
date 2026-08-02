// Package monitoring actively polls each monitor-enabled site, records check
// history, maintains the fail_streak red debounce, and serves rollup-backed
// uptime. It also owns the nightly rollup job (raw checks → check_rollup →
// cached uptime_pct). It owns no tables (the schema lives in the sites registry).
package monitoring

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
)

// Config carries the poller and uptime settings from platform config.
type Config struct {
	CheckTimeout     time.Duration
	PollConcurrency  int
	RedFailThreshold int
	UptimeWindowDays int
}

// Module implements registry.Module for the monitoring endpoints and exposes the
// background jobs (poller, rollup) to the scheduler.
type Module struct {
	store            *Store
	poller           *Poller
	rollup           *Rollup
	uptimeWindowDays int
}

// NewModule builds the monitoring module and its background jobs.
func NewModule(db *sql.DB, cfg Config, logger *slog.Logger) *Module {
	store := NewStore(db)
	return &Module{
		store:            store,
		poller:           NewPoller(db, store, cfg.CheckTimeout, cfg.PollConcurrency, cfg.RedFailThreshold, logger),
		rollup:           NewRollup(store, cfg.UptimeWindowDays, logger),
		uptimeWindowDays: cfg.UptimeWindowDays,
	}
}

// Poller exposes the poll job for the scheduler.
func (m *Module) Poller() *Poller { return m.poller }

// Rollup exposes the rollup job for the scheduler.
func (m *Module) Rollup() *Rollup { return m.rollup }

func (m *Module) Name() string { return "monitoring" }

// Migrations returns nil — the check/rollup tables live in the sites registry.
func (m *Module) Migrations() fs.FS { return nil }

// RegisterRoutes mounts the gated monitoring reads.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Get("/meta", m.getMeta)
	r.Get("/sites/{id}/checks", m.getChecks)
	r.Get("/sites/{id}/uptime", m.getUptime)
}
