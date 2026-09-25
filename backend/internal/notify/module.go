// Package notify emails Karel when something happens that the board would
// otherwise only show to whoever looks at it: a new crash group, a resolved crash
// that came back, a new feedback report, a site going down and coming back up.
//
// It is an OUTBOX. The producers — crash ingest, feedback submit, the poller —
// call its Notifier as the last statement of their own transaction, and it
// decides and queues there, with no network. A Worker, driven by the scheduler,
// folds what is queued into one digest per window and sends it after the commit,
// retrying with the same idempotency key until the provider accepts it. A
// restart, or a provider outage, loses nothing.
//
// The producers never import this package: each declares the small interface it
// calls (crash.Notifier, feedback.Notifier, monitoring.Notifier) and cmd/status
// injects this one — the direction feedback already takes with
// sites.ReportCounter.
package notify

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/ratelimit"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// Config is the module's runtime configuration.
type Config struct {
	// PublicURL is STATUS_PUBLIC_URL, without a trailing slash: every link in an
	// email is built on it.
	PublicURL string
	// From is STATUS_MAIL_FROM — the sender, which the provider must have
	// verified.
	From string
	// DigestWindow is how long the oldest pending event waits for company before
	// its digest is assembled.
	DigestWindow time.Duration
	// MaxPerHour caps digests per rolling hour. Reaching it delays, never drops.
	MaxPerHour int
	// RetentionDays is how long a settled digest stays on the deliveries list.
	RetentionDays int
}

// Module implements registry.Module: the settings page's routes. It also owns
// the Notifier the producers call and the Worker the scheduler drives.
type Module struct {
	db         *sql.DB
	sitesStore *sites.Store
	// mailer is nil when the deployment has no provider: every route still
	// serves (the page renders its "not configured" state from them), nothing is
	// queued, and the worker is not scheduled.
	mailer mail.Mailer
	cfg    Config
	logger *slog.Logger

	notifier *Notifier
	worker   *Worker
	// testLimiter bounds the test button. It is a real email per press.
	testLimiter *ratelimit.Limiter
}

// NewModule builds the module. mailer may be nil — see Module.mailer — and must
// then be an untyped nil, not a nil pointer in an interface.
func NewModule(db *sql.DB, sitesStore *sites.Store, mailer mail.Mailer, cfg Config, logger *slog.Logger) *Module {
	return &Module{
		db:          db,
		sitesStore:  sitesStore,
		mailer:      mailer,
		cfg:         cfg,
		logger:      logger,
		notifier:    &Notifier{available: mailer != nil, logger: logger},
		worker:      &Worker{db: db, mailer: mailer, cfg: cfg, logger: logger, gap: sendGap},
		testLimiter: ratelimit.New(0.1, 3, nil), // one per 10 s, three in a burst
	}
}

func (m *Module) Name() string { return "notify" }

// Migrations returns the module's own Goose files (version block 30xxx).
func (m *Module) Migrations() fs.FS { return MigrationsFS }

// Available reports whether this deployment can send at all.
func (m *Module) Available() bool { return m.mailer != nil }

// Notifier is what composition hands to crash, feedback and monitoring.
func (m *Module) Notifier() *Notifier { return m.notifier }

// Worker is what the scheduler drives.
func (m *Module) Worker() *Worker { return m.worker }

// RegisterRoutes mounts the settings page's routes. Reads need a session;
// everything that changes what is sent, or sends, needs the admin role.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Get("/notifications/settings", m.getSettings)
	r.With(httpx.RequireAdmin).Put("/notifications/settings", m.putSettings)
	r.With(httpx.RequireAdmin).Put("/notifications/muted-sites/{siteId}", m.muteSite)
	r.With(httpx.RequireAdmin).Delete("/notifications/muted-sites/{siteId}", m.unmuteSite)
	r.With(httpx.RequireAdmin).Post("/notifications/test", m.sendTest)
	r.Get("/notifications/deliveries", m.listDeliveries)
}

// Prune is the module's step in the daily job: settled digests older than the
// retention window go, with their events, and so does the downtime memory of a
// site that is no longer monitored. It is database-only.
func (m *Module) Prune(ctx context.Context, now time.Time) error {
	cutoff := ts(now.AddDate(0, 0, -m.cfg.RetentionDays))
	digests, events, states, err := prune(ctx, m.db, cutoff)
	m.logger.Info("notification prune",
		"digests_deleted", digests, "orphan_events_deleted", events, "site_states_cleared", states)
	return err
}
