// Command status is the entrypoint for the `status` monitoring & crash-reporting
// service: it loads configuration, opens the embedded SQLite database, runs
// migrations, wires Mode B auth, starts the background scheduler (poller, nightly
// rollup, daily retention), and serves the JSON API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/bootstrap"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/crash"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/monitoring"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/auth"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/config"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/retention"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/scheduler"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	// 1. Configuration — fail fast and loud.
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	logger.Info("config loaded", "config", cfg.Redacted())
	if cfg.DevAuthBypass {
		logger.Warn("AUTH BYPASS ACTIVE — ALL REQUESTS ARE FAKE-AUTHENTICATED — DO NOT DEPLOY",
			"dev_actor", cfg.DevActorID, "dev_roles", cfg.DevActorRoles)
	}

	// 2. Database: open → migrate.
	sqldb, err := appdb.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqldb.Close() }()

	migFS, err := bootstrap.MigrationFS()
	if err != nil {
		return err
	}
	if err := appdb.Migrate(sqldb, migFS); err != nil {
		return err
	}
	logger.Info("database ready", "path", cfg.DBPath)

	// 3. Mode B auth + session. status hosts login and owns its session; the
	// browser carries no token. Under the dev bypass a fixed actor is injected and
	// no auth service / session store is used, so the app runs offline.
	authConf := auth.Config{
		RoleRefresh: time.Duration(cfg.RoleRefreshMinutes) * time.Minute,
		SessionTTL:  time.Duration(cfg.SessionTTLDays) * 24 * time.Hour,
		Secure:      cfg.IsProduction(),
		Origins:     cfg.AllowedOrigins,
		Logger:      logger,
	}
	if cfg.DevAuthBypass {
		authConf.BypassActor = &reqctx.Actor{
			UserID: cfg.DevActorID,
			Type:   "user",
			Label:  cfg.DevActorID,
			Roles:  cfg.DevActorRoles,
		}
	} else {
		authConf.Sessions = auth.NewSessionStore(sqldb)
		authConf.Authr = auth.NewHTTPAuthenticator(cfg.AuthBaseURL, cfg.AuthServiceSecret, cfg.AuthJWTSecret, cfg.AuthJWTIssuer, cfg.SiteKey, logger)
	}
	authHandler := auth.NewHandler(authConf, sqldb)
	sessionMW := auth.NewSessionAuth(authConf)
	csrfMW := auth.NewCSRF(cfg.AllowedOrigins, cfg.DevAuthBypass)

	// 4. Feature modules (mounted onto the gated /api group). The sites registry is
	// the shared join point both functional modules key off. The crash module also
	// exposes a public, key-authenticated ingest surface outside the session gate.
	sitesMod := sites.NewModule(sqldb, cfg.RedFailThreshold)
	crashMod := crash.NewModule(sqldb, sitesMod.Store(), crash.Config{
		MaxIngestBytes:     cfg.MaxIngestBytes,
		IngestRatePerSec:   cfg.IngestRatePerSec,
		IngestBurst:        cfg.IngestBurst,
		RedFailThreshold:   cfg.RedFailThreshold,
		ReopenOnRegression: cfg.ReopenOnRegression,
	})
	monMod := monitoring.NewModule(sqldb, monitoring.Config{
		CheckTimeout:     cfg.CheckTimeout,
		PollConcurrency:  cfg.PollConcurrency,
		RedFailThreshold: cfg.RedFailThreshold,
		UptimeWindowDays: cfg.UptimeWindowDays,
	}, logger)
	modules := []registry.Module{sitesMod, crashMod, monMod}

	// 5. Background scheduler: the poller every CHECK_INTERVAL, and a daily closure
	// that rolls up yesterday, refreshes cached uptime, then purges (retention
	// appended in M4). The rollup-before-purge order is enforced inside the daily
	// closure. On boot the same RunDaily backfills any missed rollup days and
	// refreshes the uptime cache, so a restart after an outage never leaves a stale
	// uptime figure or lets the purge delete un-aggregated checks.
	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	var jobsWG sync.WaitGroup
	defer cancelJobs()
	purger := retention.NewPurger(sqldb, cfg.RetentionDays, cfg.RollupRetentionDays, logger)
	if err := monMod.Rollup().RunDaily(jobsCtx, time.Now().UTC()); err != nil {
		logger.Error("rollup catch-up on boot", "err", err)
	}
	scheduler.Start(jobsCtx, &jobsWG, scheduler.Config{
		CheckInterval: cfg.CheckInterval,
		DailyAtHour:   cfg.DailyAtHour,
		DailyAtMin:    cfg.DailyAtMin,
		Logger:        logger,
	}, scheduler.Jobs{
		Poll: monMod.Poller().RunOnce,
		Daily: func(ctx context.Context) {
			now := time.Now().UTC()
			// Order matters: roll up + refresh the uptime cache BEFORE purging, so no
			// raw check is deleted before it is aggregated (a single sequential
			// closure guarantees the ordering). If the rollup fails, SKIP the purge so
			// the un-aggregated checks survive to be retried on the next run rather
			// than being deleted, leaving a permanent uptime gap.
			if err := monMod.Rollup().RunDaily(ctx, now); err != nil {
				logger.Error("daily rollup", "err", err)
				return
			}
			if err := purger.Purge(ctx, now); err != nil {
				logger.Error("retention purge", "err", err)
			}
		},
	})

	// 6. HTTP server.
	handler := httpx.NewRouter(httpx.Deps{
		Logger:            logger,
		DB:                sqldb,
		Site:              cfg.SiteKey,
		InsecureAuth:      cfg.DevAuthBypass,
		TrustedProxyCount: cfg.TrustedProxyCount,
		MountAuth:         func(api chi.Router) { authHandler.Mount(api, csrfMW) },
		MountPublicAPI:    func(api chi.Router) { crashMod.RegisterPublicRoutes(api) },
		SessionMW:         sessionMW,
		CSRFMW:            csrfMW,
		MountAPI:          func(api chi.Router) { registry.MountAll(api, modules) },
		StaticDir:         cfg.StaticDir,
	})
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 7. Serve until interrupted, then shut down gracefully.
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		return err
	case <-stop:
		logger.Info("shutting down")
		cancelJobs()
		jobsWG.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}
