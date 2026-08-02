package auth

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
)

// Login rate-limit: per email and per IP.
const (
	loginMaxAttempts = 10
	loginWindow      = 15 * time.Minute
)

// Handler serves the status-hosted /api/auth endpoints (Mode B).
type Handler struct {
	cfg     Config
	db      *sql.DB
	limiter *rateLimiter
}

// NewHandler builds the auth handler.
func NewHandler(cfg Config, db *sql.DB) *Handler {
	return &Handler{cfg: cfg, db: db, limiter: newRateLimiter(loginMaxAttempts, loginWindow, cfg.Now)}
}

// Mount registers the auth routes on the /api router (OUTSIDE the session gate —
// login must work before there is a session). csrf is applied to logout only;
// login is CSRF-exempt but rate-limited, and the session probe is a safe GET.
func (h *Handler) Mount(api chi.Router, csrf func(http.Handler) http.Handler) {
	api.Post("/auth/login", h.login)
	api.Get("/auth/session", h.session)
	api.With(csrf).Post("/auth/logout", h.logout)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userPublic struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	DisplayName *string  `json:"display_name"`
	Roles       []string `json:"roles"`
}

type sessionUser struct {
	User userPublic `json:"user"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}

	// Under the dev bypass there is no session store or auth service (both nil):
	// accept the login and echo the bypass actor so the login screen is exercisable
	// offline. Return before any rate-limit or Sessions access.
	if h.cfg.BypassActor != nil {
		a := h.cfg.BypassActor
		id := Identity{UserID: a.UserID, Email: firstNonEmpty(in.Email, a.Label, a.UserID), DisplayName: a.Label, Roles: a.Roles}
		httpx.JSON(w, http.StatusOK, sessionUser{User: publicUser(id)})
		return
	}

	ip := clientIP(r)
	// Rate-limit FAILED logins only (a successful sign-in resets both counters, so a
	// user's own repeated valid logins never trip it):
	//   - ipKey caps password spraying across many accounts from one IP.
	//   - acctKey caps brute force against a single account FROM ONE IP and is keyed
	//     on ip|email — NOT the email alone (the home/fin pattern). Keying on the
	//     email alone would let an attacker from any address burn a victim's failure
	//     budget and lock the legitimate owner out of their own account (a targeted
	//     lockout DoS); scoping it to the source IP preserves the invariant that a
	//     user can never be locked out by traffic that isn't theirs. Distributed
	//     brute force across many IPs is bounded per-IP here and by upstream password
	//     hashing / MFA at the auth service.
	ipKey := "ip:" + ip
	acctKey := ipKey + "|email:" + normalizeEmail(in.Email)
	if !h.limiter.allowed(ipKey) || !h.limiter.allowed(acctKey) {
		httpx.WriteError(w, &httpx.APIError{Status: http.StatusTooManyRequests, Code: "too_many_requests", Detail: "too many attempts, try again later"})
		return
	}

	id, err := h.cfg.Authr.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		if errors.Is(err, ErrBadCredentials) {
			h.limiter.fail(ipKey)
			h.limiter.fail(acctKey)
		}
		writeLoginError(w, err)
		return
	}
	h.limiter.reset(ipKey)
	h.limiter.reset(acctKey)

	now := h.cfg.now()
	req, _ := reqctx.RequestFrom(r.Context())

	var rawToken string
	if err := appdb.WithTx(r.Context(), h.db, func(tx *sql.Tx) error {
		raw, _, err := h.cfg.Sessions.Create(r.Context(), tx, id, req.UserAgent, ip, h.cfg.SessionTTL, now)
		if err != nil {
			return err
		}
		rawToken = raw
		return nil
	}); err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}

	csrfToken, err := newCSRFToken()
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	setAuthCookies(w, rawToken, csrfToken, h.cfg.SessionTTL, h.cfg.Secure)
	httpx.JSON(w, http.StatusOK, sessionUser{User: publicUser(id)})
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	if h.cfg.BypassActor != nil {
		a := h.cfg.BypassActor
		httpx.JSON(w, http.StatusOK, sessionUser{User: publicUser(Identity{
			UserID: a.UserID, Email: a.Label, DisplayName: a.Label, Roles: a.Roles,
		})})
		return
	}
	c, err := r.Cookie(cookieSession)
	if err != nil || c.Value == "" {
		httpx.WriteError(w, httpx.ErrUnauthorized("no session"))
		return
	}
	sess, ok, err := h.cfg.Sessions.Lookup(r.Context(), c.Value, h.cfg.now())
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !ok {
		clearAuthCookies(w, h.cfg.Secure)
		httpx.WriteError(w, httpx.ErrUnauthorized("invalid or expired session"))
		return
	}
	httpx.JSON(w, http.StatusOK, sessionUser{User: publicUser(Identity{
		UserID: sess.UserID, Email: sess.Email, DisplayName: sess.DisplayName, Roles: sess.Roles,
	})})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if h.cfg.BypassActor != nil {
		clearAuthCookies(w, h.cfg.Secure)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c, err := r.Cookie(cookieSession)
	if err != nil || c.Value == "" {
		httpx.WriteError(w, httpx.ErrUnauthorized("no session"))
		return
	}
	now := h.cfg.now()
	sess, ok, err := h.cfg.Sessions.Lookup(r.Context(), c.Value, now)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !ok {
		clearAuthCookies(w, h.cfg.Secure)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = sess
	if err := appdb.WithTx(r.Context(), h.db, func(tx *sql.Tx) error {
		_, _, _, err := h.cfg.Sessions.RevokeByToken(r.Context(), tx, c.Value, now)
		return err
	}); err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	clearAuthCookies(w, h.cfg.Secure)
	w.WriteHeader(http.StatusNoContent)
}

func writeLoginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBadCredentials):
		httpx.WriteError(w, httpx.ErrUnauthorized("invalid email or password"))
	case errors.Is(err, ErrDisabled):
		httpx.WriteError(w, httpx.ErrForbidden("account is disabled or has no access"))
	case errors.Is(err, ErrMFARequired):
		httpx.WriteError(w, &httpx.APIError{Status: http.StatusConflict, Code: "mfa_required", Detail: "complete sign-in at auth.tilcer.cz"})
	default: // ErrUnreachable / anything else
		httpx.WriteError(w, &httpx.APIError{Status: http.StatusBadGateway, Code: "auth_unreachable", Detail: "the auth service is unavailable"})
	}
}

func publicUser(id Identity) userPublic {
	var dn *string
	if id.DisplayName != "" {
		d := id.DisplayName
		dn = &d
	}
	roles := id.Roles
	if roles == nil {
		roles = []string{}
	}
	return userPublic{ID: id.UserID, Email: id.Email, DisplayName: dn, Roles: roles}
}

// normalizeEmail canonicalizes the client-supplied email for rate-limit keying.
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// clientIP mirrors httpx.clientIP for rate-limit keying (X-Forwarded-For aware).
func clientIP(r *http.Request) string {
	if info, ok := reqctx.RequestFrom(r.Context()); ok && info.IP != "" {
		return info.IP
	}
	return r.RemoteAddr
}
