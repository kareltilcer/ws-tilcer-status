package httpx

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Deps carries everything the router needs. It grows as modules land without
// churning call sites.
type Deps struct {
	Logger       *slog.Logger
	DB           Pinger
	Site         string
	InsecureAuth bool
	// TrustedProxyCount is how many XFF-appending reverse proxies sit in front of
	// this service; it governs client-IP resolution for logs and rate limiting
	// (see clientIP). 0 keys on the direct peer; the Coolify default is 1 (Traefik).
	TrustedProxyCount int
	// MountAuth mounts the public /api/auth/* endpoints (login/logout/session).
	// These run OUTSIDE the session gate — login must work before a session exists
	// (Mode B). May be nil.
	MountAuth func(api chi.Router)
	// MountPublicAPI mounts public /api endpoints that authenticate themselves
	// (the per-site crash ingest, POST /api/ingest/{siteId}). It runs OUTSIDE the
	// session gate. May be nil.
	MountPublicAPI func(api chi.Router)
	// SessionMW authorizes every gated /api request from the session cookie.
	// Applied to the gated group only. May be nil (tests).
	SessionMW func(http.Handler) http.Handler
	// CSRFMW enforces the double-submit CSRF check on cookie-authenticated
	// mutations within the gated group. May be nil (tests).
	CSRFMW func(http.Handler) http.Handler
	// MountAPI mounts feature modules onto the authenticated, CSRF-protected /api
	// group. Each module composes its own routes here.
	MountAPI func(api chi.Router)
	// StaticDir, when non-empty, is the directory of the built SPA served on all
	// non-API routes with an index.html fallback (local single-origin harness only).
	StaticDir string
}

// NewRouter assembles the HTTP handler: baseline middleware, public health
// probes, the public auth + ingest endpoints, the session-gated + CSRF-protected
// /api surface, and the SPA fallback. Health probes are mounted OUTSIDE auth so
// they stay public and cheap.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(StripAPIPrefix)
	r.Use(RequestID(d.Site, d.TrustedProxyCount))
	r.Use(Logger(d.Logger))
	r.Use(Recover(d.Logger))

	// Health (public).
	r.Get("/healthz", Healthz())
	r.Get("/readyz", Readyz(d.DB, d.InsecureAuth))

	r.Route("/api", func(api chi.Router) {
		// Public auth endpoints (login is pre-session, Mode B).
		if d.MountAuth != nil {
			d.MountAuth(api)
		}
		// Public, self-authenticating endpoints (crash ingest by per-site key).
		if d.MountPublicAPI != nil {
			d.MountPublicAPI(api)
		}
		// Everything else is authorized from the session cookie and, for
		// mutations, CSRF-protected.
		api.Group(func(gated chi.Router) {
			if d.SessionMW != nil {
				gated.Use(d.SessionMW)
			}
			if d.CSRFMW != nil {
				gated.Use(d.CSRFMW)
			}
			if d.MountAPI != nil {
				d.MountAPI(gated)
			}
		})
	})

	// Catch-all: the built SPA is served on non-API routes with an index.html
	// fallback. An unmatched /api/** path must NOT fall through to the SPA shell —
	// it gets a JSON 404 so a mistyped endpoint fails loudly.
	var spa http.Handler
	if d.StaticDir != "" {
		spa = SPAHandler(d.StaticDir)
	}
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if spa != nil && !strings.HasPrefix(req.URL.Path, "/api/") {
			spa.ServeHTTP(w, req)
			return
		}
		WriteError(w, ErrNotFound("no such endpoint"))
	})

	return r
}
