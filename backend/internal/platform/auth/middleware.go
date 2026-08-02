package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
)

// Cookie names (host-only on status.tilcer.cz).
const (
	cookieSession = "session"
	cookieCSRF    = "csrf"
)

// touchGranularity bounds how often the middleware writes to slide a session's
// expiry, so a request doesn't issue a write every time.
const touchGranularity = time.Hour

// Config configures the session middleware (and is reused by the /api/auth
// handler). In development BypassActor is set and the session store / auth client
// are nil: every request is authenticated as that actor (the app runs offline).
type Config struct {
	Sessions    *SessionStore
	Authr       Authenticator
	RoleRefresh time.Duration // re-mint threshold (STATUS_ROLE_REFRESH_MINUTES)
	SessionTTL  time.Duration // sliding window (STATUS_SESSION_TTL_DAYS)
	Secure      bool          // Secure cookie attribute (TLS-only)
	Origins     []string      // CSRF Origin allowlist (STATUS_ALLOWED_ORIGINS)
	BypassActor *reqctx.Actor // dev bypass; nil in production
	Now         func() time.Time
	Logger      *slog.Logger
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// NewSessionAuth returns middleware that authorizes every request from the status
// session cookie, refreshing roles by re-minting past the threshold and failing
// closed when the user is disabled/deleted in auth. It runs on the gated /api
// group (everything except /api/auth/* and the public ingest). Under the dev
// bypass it injects a fixed actor and skips the session entirely.
func NewSessionAuth(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.BypassActor != nil {
				next.ServeHTTP(w, r.WithContext(reqctx.WithActor(r.Context(), *cfg.BypassActor)))
				return
			}
			c, err := r.Cookie(cookieSession)
			if err != nil || c.Value == "" {
				httpx.WriteError(w, httpx.ErrUnauthorized("no session"))
				return
			}
			now := cfg.now()
			sess, ok, err := cfg.Sessions.Lookup(r.Context(), c.Value, now)
			if err != nil {
				httpx.WriteError(w, httpx.ErrInternal(""))
				return
			}
			if !ok {
				clearAuthCookies(w, cfg.Secure)
				httpx.WriteError(w, httpx.ErrUnauthorized("invalid or expired session"))
				return
			}

			roles := sess.Roles
			if cfg.Authr != nil && now.Sub(sess.RolesRefreshedAt) > cfg.RoleRefresh {
				id, mintErr := cfg.Authr.Mint(r.Context(), sess.UserID)
				switch {
				case errors.Is(mintErr, ErrUserClosed):
					// Fail closed: the user was disabled/deleted in auth.
					_ = cfg.Sessions.RevokeByID(r.Context(), sess.ID)
					clearAuthCookies(w, cfg.Secure)
					httpx.WriteError(w, httpx.ErrUnauthorized("session revoked"))
					return
				case mintErr == nil:
					roles = id.Roles
					_ = cfg.Sessions.RefreshRoles(r.Context(), sess.ID, roles, now)
				default:
					// Transient auth outage: keep cached roles, retry next request.
					cfg.logger().Warn("role re-mint failed (transient)", "user", sess.UserID, "err", mintErr)
				}
			}

			// Ensure the JS-readable csrf cookie exists. It is minted only at login,
			// so a browser that dropped it (eviction, an intermediary, a manual clear)
			// while keeping the HttpOnly session would still look authenticated yet
			// 403 on every mutation with no way back. Re-mint it here so any safe
			// request (the board polls every 30s) restores double-submit before the
			// user acts — and since this middleware runs before the CSRF gate, even a
			// rejected mutation carries the fresh cookie so its retry succeeds.
			csrf, csrfErr := r.Cookie(cookieCSRF)
			csrfPresent := csrfErr == nil && csrf.Value != ""
			if !csrfPresent {
				if token, err := newCSRFToken(); err == nil {
					setCSRFCookie(w, token, cfg.SessionTTL, cfg.Secure)
				}
			}

			// Slide the expiry lazily. The DB slide alone is not enough: re-issue the
			// browser cookies with a fresh lifetime or they'd still expire at
			// login+SessionTTL regardless of activity. A csrf cookie just minted above
			// already carries the full TTL, so only refresh a pre-existing one here.
			if now.Sub(sess.LastSeenAt) > touchGranularity {
				if err := cfg.Sessions.Touch(r.Context(), sess.ID, now, now.Add(cfg.SessionTTL)); err == nil {
					setSessionCookie(w, c.Value, cfg.SessionTTL, cfg.Secure)
					if csrfPresent {
						setCSRFCookie(w, csrf.Value, cfg.SessionTTL, cfg.Secure)
					}
				}
			}

			actor := reqctx.Actor{
				UserID: sess.UserID,
				Type:   "user",
				Label:  labelFor(sess),
				Roles:  roles,
			}
			next.ServeHTTP(w, r.WithContext(reqctx.WithActor(r.Context(), actor)))
		})
	}
}

// NewCSRF returns double-submit CSRF middleware: every cookie-authenticated
// state-changing request must carry an X-CSRF-Token header equal to the csrf
// cookie AND an Origin/Referer within the allowlist. Safe methods pass through.
// Under the dev bypass it is a no-op.
func NewCSRF(origins []string, bypass bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bypass || safeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if !originAllowed(r, origins) {
				httpx.WriteError(w, httpx.ErrForbidden("origin not allowed"))
				return
			}
			header := r.Header.Get("X-CSRF-Token")
			cookie, err := r.Cookie(cookieCSRF)
			if header == "" || err != nil || cookie.Value == "" || header != cookie.Value {
				httpx.WriteError(w, httpx.ErrForbidden("csrf token mismatch"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// originAllowed checks the request's Origin (or, absent that, Referer) host
// against the allowlist. Entries may be exact origins ("https://status.tilcer.cz")
// or wildcards ("https://*.tilcer.cz").
func originAllowed(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}
	if origin == "" {
		return false // cannot verify a cookie-authenticated mutation
	}
	for _, a := range allowed {
		if originMatches(origin, a) {
			return true
		}
	}
	return false
}

func originMatches(origin, pattern string) bool {
	if origin == pattern {
		return true
	}
	scheme, host, ok := splitOrigin(origin)
	pScheme, pHost, ok2 := splitOrigin(pattern)
	if !ok || !ok2 || scheme != pScheme {
		return false
	}
	if strings.HasPrefix(pHost, "*.") {
		suffix := pHost[1:] // ".tilcer.cz"
		return strings.HasSuffix(host, suffix) && host != suffix[1:]
	}
	return false
}

func splitOrigin(o string) (scheme, host string, ok bool) {
	i := strings.Index(o, "://")
	if i < 0 {
		return "", "", false
	}
	return o[:i], o[i+3:], true
}

func labelFor(s Session) string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Email
}

// setAuthCookies sets the session (HttpOnly) and csrf (JS-readable) cookies,
// host-only on status.tilcer.cz (no Domain), Secure + SameSite=Lax.
func setAuthCookies(w http.ResponseWriter, sessionToken, csrfToken string, ttl time.Duration, secure bool) {
	setSessionCookie(w, sessionToken, ttl, secure)
	setCSRFCookie(w, csrfToken, ttl, secure)
}

func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieSession, Value: token, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

func setCSRFCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieCSRF, Value: token, Path: "/",
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

func clearAuthCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{cookieSession, cookieCSRF} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/",
			HttpOnly: name == cookieSession, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
		})
	}
}

// newCSRFToken mints a random token for the csrf cookie (double-submit).
func newCSRFToken() (string, error) {
	raw, _, err := newToken()
	return raw, err
}
