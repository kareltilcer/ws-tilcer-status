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
	// AllowedOrigins is STATUS_ALLOWED_ORIGINS — the one origin allow-list this
	// service has (V3-D50). It governs CORS on the public group here and the
	// Origin check in the CSRF gate. Empty disables cross-origin access entirely.
	AllowedOrigins []string
}

// Router is the composed HTTP handler. It carries the underlying chi mux so the
// routing tree can be enumerated rather than re-listed by hand — APISegments
// derives the StripAPIPrefix set from it, and the routing test walks it to prove
// every registered API route survives a stripped prefix.
type Router struct {
	http.Handler
	Mux *chi.Mux
}

// NewRouter assembles the HTTP handler: baseline middleware, public health
// probes, the public auth + ingest endpoints (cross-origin, with preflights), the
// session-gated + CSRF-protected /api surface, and the SPA fallback. Health
// probes are mounted OUTSIDE auth so they stay public and cheap.
//
// StripAPIPrefix is applied last, wrapping the finished mux, because its
// re-prefix set is derived from the routes registered below.
func NewRouter(d Deps) *Router {
	r := chi.NewRouter()

	r.Use(RequestID(d.Site, d.TrustedProxyCount))
	r.Use(Logger(d.Logger))
	r.Use(Recover(d.Logger))

	// Health (public).
	r.Get("/healthz", Healthz())
	r.Get("/readyz", Readyz(d.DB, d.InsecureAuth))

	cors := NewCORS(d.AllowedOrigins)
	r.Route("/api", func(api chi.Router) {
		// The public group: endpoints that authenticate themselves (or, for login,
		// authenticate nothing yet) and are therefore the only ones reachable
		// cross-origin. CORS applies here and nowhere else.
		api.Group(func(pub chi.Router) {
			pub.Use(cors)
			// Public auth endpoints (login is pre-session, Mode B).
			if d.MountAuth != nil {
				d.MountAuth(pub)
			}
			// Public, self-authenticating endpoints (crash ingest by per-site key).
			if d.MountPublicAPI != nil {
				d.MountPublicAPI(pub)
			}
			// A preflight on every public path, derived from what the mounts above
			// just registered — chi only runs group middleware on a MATCHED route,
			// so an unrouted OPTIONS would fall through to the JSON 404 below with
			// no CORS headers and the browser would block the real request. The
			// gated group is registered after this, so the walk sees public routes
			// only.
			for _, pattern := range preflightPatterns(api) {
				pub.Options(pattern, Preflight)
			}
		})
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

	return &Router{Handler: StripAPIPrefix(APISegments(r))(r), Mux: r}
}

// preflightPatterns returns the distinct route patterns registered on routes that
// do not already answer OPTIONS. It is called mid-construction, with only the
// public group mounted, so its result is exactly the set of public paths.
func preflightPatterns(routes chi.Routes) []string {
	methods := map[string]map[string]bool{}
	var order []string
	_ = chi.Walk(routes, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if methods[route] == nil {
			methods[route] = map[string]bool{}
			order = append(order, route)
		}
		methods[route][method] = true
		return nil
	})
	out := make([]string, 0, len(order))
	for _, pattern := range order {
		if !methods[pattern][http.MethodOptions] {
			out = append(out, pattern)
		}
	}
	return out
}
