package httpx

import (
	"context"
	"net/http"
	"time"
)

// health is the response body for the probes (openapi.yaml schema Health).
type health struct {
	Status string `json:"status"`
	// InsecureAuth is surfaced on /readyz when the dev auth bypass is active, so a
	// bypass build can never be mistaken for a real deployment.
	InsecureAuth bool `json:"insecure_auth,omitempty"`
}

// Pinger is satisfied by *sql.DB (and test doubles).
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Healthz reports process liveness. Public, no auth.
func Healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, health{Status: "ok"})
	}
}

// Readyz reports readiness, including a SQLite ping. Returns 503 if the database
// is unreachable. insecureAuth marks a dev-bypass build.
func Readyz(db Pinger, insecureAuth bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			JSON(w, http.StatusServiceUnavailable, Error{Err: "not_ready", Detail: "database unavailable"})
			return
		}
		JSON(w, http.StatusOK, health{Status: "ok", InsecureAuth: insecureAuth})
	}
}
