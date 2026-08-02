package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

// httpAuthenticator talks to the shared auth service BE→BE as the `status`
// service client, authenticating with the X-Service-Secret header.
//
// Wire contract (ws-tilcer-auth): both endpoints return a `TokenResponse` — a
// site-scoped HS256 access token, NOT a plain identity envelope. The user id,
// email, display name, and roles live in the JWT CLAIMS (sub / email / name /
// roles), so status must verify the token and read the claims. auth mints
// roles:["*"] for superusers.
//
//	POST {base}/internal/login
//	  headers: X-Service-Secret: <secret>
//	  body:    {"email": "...", "password": "...", "site": "status"}
//	  200 -> {"access_token": "<JWT>", "token_type": "Bearer", "expires_in": n, "site": "status"}
//	         (or {"mfa_required": true, ...}; status does not handle MFA)
//	  401 -> bad credentials
//	  403 -> disabled / unverified / no access to site
//	  5xx -> unreachable
//
//	POST {base}/internal/token/mint
//	  headers: X-Service-Secret: <secret>
//	  body:    {"user_id": "...", "site": "status"}
//	  200 -> {"access_token": "<JWT>", ...} (same TokenResponse)
//	  403/404/409 -> user closed (disabled/deleted/unverified)
//	  5xx -> unreachable (transient)
type httpAuthenticator struct {
	baseURL   string       // where status CALLS auth (e.g. https://auth.tilcer.cz)
	secret    string       // X-Service-Secret (BE→BE)
	jwtSecret []byte       // shared HS256 secret used to VERIFY the returned access token
	issuer    string       // expected token `iss`; "" = do not enforce
	site      string       // expected JWT audience + `site` claim
	client    *http.Client //
	logger    *slog.Logger //
}

// NewHTTPAuthenticator returns an Authenticator backed by the auth service.
//   - jwtSecret is the shared HS256 signing secret (STATUS_AUTH_JWT_SECRET); it
//     must equal the auth service's JWT secret.
//   - issuer, when non-empty, is the exact `iss` claim required on returned
//     tokens. Empty disables the issuer check; the signature + audience still
//     bind the token to auth and this site.
func NewHTTPAuthenticator(baseURL, serviceSecret, jwtSecret, issuer, site string, logger *slog.Logger) Authenticator {
	return &httpAuthenticator{
		baseURL:   baseURL,
		secret:    serviceSecret,
		jwtSecret: []byte(jwtSecret),
		issuer:    issuer,
		site:      site,
		client:    &http.Client{Timeout: 5 * time.Second},
		logger:    logger,
	}
}

func (h *httpAuthenticator) log() *slog.Logger {
	if h.logger != nil {
		return h.logger
	}
	return slog.Default()
}

// tokenResponse is auth's TokenResponse envelope. The identity + roles are inside
// AccessToken (a JWT), not in this struct.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Site        string `json:"site"`
	// mfaRequired is set on the /internal/login MFA-challenge branch; status does
	// not handle MFA, so its presence is treated as ErrMFARequired.
	MFARequired bool `json:"mfa_required"`
}

// accessClaims mirrors the site-scoped access token's claim set. Only the fields
// status needs are named; RegisteredClaims carries sub / iss / aud / exp.
type accessClaims struct {
	Site  string   `json:"site"`
	Roles []string `json:"roles"`
	Email string   `json:"email"`
	Name  string   `json:"name"`
	jwt.RegisteredClaims
}

// verifyToken parses and cryptographically verifies raw (HS256 + expiry +
// audience==site), then returns the identity carried in its claims. A superuser
// arrives as Roles == ["*"].
func (h *httpAuthenticator) verifyToken(raw string) (Identity, error) {
	claims := &accessClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		// Reject anything but HS256 up front — never let the token's own header
		// choose the algorithm (guards against alg-confusion / alg=none).
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		return h.jwtSecret, nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
		jwt.WithAudience(h.site), // token minted for another site cannot be replayed here
	)
	if err != nil {
		return Identity{}, fmt.Errorf("verify access token: %w", err)
	}
	// Defense in depth: the `site` claim must also match our site key.
	if claims.Site != h.site {
		return Identity{}, fmt.Errorf("token site claim %q != %q", claims.Site, h.site)
	}
	// Optional issuer pin. Compared tolerant of a trailing slash. Empty = skip.
	if want := strings.TrimRight(h.issuer, "/"); want != "" {
		if got := strings.TrimRight(claims.Issuer, "/"); got != want {
			return Identity{}, fmt.Errorf("token issuer %q != %q", claims.Issuer, want)
		}
	}
	return Identity{
		UserID:      claims.Subject,
		Email:       claims.Email,
		DisplayName: claims.Name,
		Roles:       claims.Roles,
	}, nil
}

func (h *httpAuthenticator) Login(ctx context.Context, email, password string) (Identity, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password, "site": h.site})
	resp, err := h.post(ctx, "/internal/login", body)
	if err != nil {
		return Identity{}, ErrUnreachable
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var tr tokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
			return Identity{}, ErrUnreachable
		}
		// A TOTP-enabled account answers 200 with an MFA challenge instead of a
		// token; status does not handle MFA.
		if tr.MFARequired || tr.AccessToken == "" {
			return Identity{}, ErrMFARequired
		}
		id, err := h.verifyToken(tr.AccessToken)
		if err != nil {
			// A token we cannot verify means a broken integration (wrong shared
			// secret, wrong issuer/audience) or a hostile response — fail closed.
			h.log().Error("login: access token verification failed", "err", err)
			return Identity{}, ErrUnreachable
		}
		return id, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return Identity{}, ErrBadCredentials
	case resp.StatusCode == http.StatusForbidden:
		return Identity{}, ErrDisabled
	case resp.StatusCode == http.StatusConflict:
		return Identity{}, ErrMFARequired
	default:
		return Identity{}, ErrUnreachable
	}
}

func (h *httpAuthenticator) Mint(ctx context.Context, userID string) (Identity, error) {
	body, _ := json.Marshal(map[string]string{"user_id": userID, "site": h.site})
	resp, err := h.post(ctx, "/internal/token/mint", body)
	if err != nil {
		return Identity{}, ErrUnreachable
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var tr tokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
			return Identity{}, ErrUnreachable
		}
		id, err := h.verifyToken(tr.AccessToken)
		if err != nil {
			// Treat an unverifiable mint like a transient failure: the caller keeps
			// the session's cached roles and retries on the next interval.
			h.log().Error("mint: access token verification failed", "err", err)
			return Identity{}, ErrUnreachable
		}
		if id.UserID == "" {
			id.UserID = userID
		}
		return id, nil
	case resp.StatusCode == http.StatusForbidden,
		resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusConflict:
		return Identity{}, ErrUserClosed
	default:
		return Identity{}, ErrUnreachable
	}
}

func (h *httpAuthenticator) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Secret", h.secret)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth %s: %w", path, err)
	}
	return resp, nil
}
