// Package feedback is the fourth module: users of the monitored apps file bug
// reports from inside them, with image or video attachments in R2, and Karel
// triages what arrives in one cross-site inbox.
//
// It is the first module that owns a migration block (20xxx) — four tables of its
// own, and no column added to `site` (V3-D02). Like `crash` it mounts twice: the
// registry hands out the authenticated router only, so the three public widget
// routes go through httpx.Deps.MountPublicAPI (V3-D52).
package feedback

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback/blob"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/ratelimit"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// stringsVersion is bumped when the widget's bundled CZ/EN strings change. The
// widget ships its own copy; this exists only so a mismatch is diagnosable.
const stringsVersion = 1

// objectPrefix is the only prefix this module ever writes to or deletes under.
// ⚠ The sweep's delete step is scoped to it, and nothing widens that scope.
const objectPrefix = "feedback/"

// configRateFactor is how much larger the dialog-open budget is than the
// reporting budget.
//
// The widget fetches its configuration on every page load, because it must render
// nothing at all until the answer arrives (V3-D35) — so config traffic is
// browsing volume, whereas STATUS_FEEDBACK_RATE (≈20/hour) is reporting volume.
// Charging page loads to the reporting bucket would exhaust it during ordinary
// browsing and leave the launcher missing, which is the one failure mode the
// design is most careful to avoid. This bucket exists to bound ticket rows and
// writes, not to bound reports; it is derived from the configured rates so a
// deployment that raises those raises this too.
const configRateFactor = 20

// Config is the module's runtime configuration (PRD §V3-9).
type Config struct {
	// Enabled reports whether the deployment has object storage configured. When
	// false the module still serves its gated routes — the dashboard renders the
	// switch and PATCH answers 503 — but every site reads as disabled at the
	// public surface: a switch must not be flippable into a state the process
	// cannot serve.
	Enabled bool

	MaxFiles      int
	MaxImageBytes int64
	MaxVideoBytes int64
	MaxTextBytes  int64

	RatePerSec   float64 // per widget key
	Burst        int
	IPRatePerSec float64 // per client IP, across all sites
	IPBurst      int

	UploadTTL    time.Duration
	ViewTTL      time.Duration
	UnclaimedTTL time.Duration
	MinDwell     time.Duration

	TicketSecret string
	IPHashSalt   string

	// AllowedOrigins is STATUS_ALLOWED_ORIGINS — the one origin allow-list this
	// service has (V3-D50). A widget submission whose Origin is outside it is a
	// 403; a request with no Origin at all is not a browser and is not judged by
	// this check, which is why the allow-list sits fifth in the abuse controls and
	// not first.
	AllowedOrigins []string
}

// Module implements registry.Module (the gated inbox and per-site configuration)
// plus the public, widget-key-authenticated surface mounted outside the session
// gate.
type Module struct {
	db         *sql.DB
	store      *Store
	sitesStore *sites.Store
	blobs      blob.Store
	cfg        Config
	logger     *slog.Logger

	// Submission limiters: one per widget key, one per client IP (V3-D23 — the
	// client IP comes from the same XFF machinery the logs and the login limiter
	// use, never a second parser).
	keyLimiter *ratelimit.Limiter
	ipLimiter  *ratelimit.Limiter
	// Dialog-open limiters guarding the config route; see configRateFactor.
	configKeyLimiter *ratelimit.Limiter
	configIPLimiter  *ratelimit.Limiter
}

// NewModule builds the feedback module. blobs may be nil when the deployment has
// no object storage; the module then serves its gated routes and reports every
// site as disabled.
func NewModule(db *sql.DB, sitesStore *sites.Store, blobs blob.Store, cfg Config, logger *slog.Logger) *Module {
	return &Module{
		db:         db,
		store:      NewStore(db),
		sitesStore: sitesStore,
		blobs:      blobs,
		cfg:        cfg,
		logger:     logger,

		keyLimiter:       ratelimit.New(cfg.RatePerSec, cfg.Burst, nil),
		ipLimiter:        ratelimit.New(cfg.IPRatePerSec, cfg.IPBurst, nil),
		configKeyLimiter: ratelimit.New(cfg.RatePerSec*configRateFactor, cfg.Burst*configRateFactor, nil),
		configIPLimiter:  ratelimit.New(cfg.IPRatePerSec*configRateFactor, cfg.IPBurst*configRateFactor, nil),
	}
}

func (m *Module) Name() string { return "feedback" }

// Migrations returns the module's own Goose files (version block 20xxx).
func (m *Module) Migrations() fs.FS { return MigrationsFS }

// Store exposes the persistence layer so composition can hand it to the sites
// registry as the report counter and the object-key collector.
func (m *Module) Store() *Store { return m.store }

// RegisterRoutes mounts the gated inbox, triage and per-site configuration.
// Reads need a session; mutations need the admin role.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Get("/reports", m.listReports)
	r.Get("/reports/{ref}", m.getReport)
	r.With(httpx.RequireAdmin).Patch("/reports/{ref}", m.patchReport)
	r.With(httpx.RequireAdmin).Delete("/reports/{ref}", m.deleteReport)
	r.Get("/reports/{ref}/attachments/{attachmentId}/url", m.attachmentURL)
	r.Get("/sites/{id}/feedback-config", m.getSiteConfig)
	r.With(httpx.RequireAdmin).Patch("/sites/{id}/feedback-config", m.patchSiteConfig)
	r.With(httpx.RequireAdmin).Post("/sites/{id}/rotate-widget-key", m.rotateWidgetKey)
}

// RegisterPublicRoutes mounts the three public, widget-key-authenticated routes.
// They are mounted OUTSIDE the session gate (via httpx.Deps.MountPublicAPI) and
// are the only routes of this module that answer a cross-origin request.
//
// ⚠ They sit under /api/ingest/ deliberately (V3-D12): that prefix is already on
// StripAPIPrefix's derived re-prefix set and demonstrably works in production, so
// the reporting path is the half least likely to be broken by a proxy setting.
func (m *Module) RegisterPublicRoutes(api chi.Router) {
	api.Get("/ingest/{siteId}/feedback/config", m.widgetConfig)
	api.Post("/ingest/{siteId}/feedback", m.submit)
	api.Post("/ingest/{siteId}/feedback/{ref}/claim", m.claim)
}

// storageReady reports whether this deployment can actually serve a report end to
// end. Without object storage there is no way to mint an upload URL, so the
// public surface reports every site as disabled rather than accepting reports
// whose attachments could never exist.
func (m *Module) storageReady() bool { return m.cfg.Enabled && m.blobs != nil }
