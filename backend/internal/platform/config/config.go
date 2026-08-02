// Package config loads and validates the service configuration from the
// environment (PRD §9). It fails fast and loudly: a missing required variable
// or an invalid value aborts startup with a message listing every problem —
// a silently-defaulted secret is worse than a crash.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-validated runtime configuration. All fields are safe to
// read concurrently after Load returns.
type Config struct {
	// Env is the deployment environment: "development" (default) or "production".
	// It gates the dev auth bypass — the bypass is refused outright in production.
	Env string

	// Addr is the TCP listen address for the HTTP server, e.g. ":112".
	Addr string

	// DBPath is the SQLite database file path (persisted volume in production).
	DBPath string

	// StaticDir is the directory of the built SPA the server serves on non-API
	// routes. Empty in the two-app deploy (API-only image) and in development.
	StaticDir string

	// SiteKey is the auth site key this service authenticates against ("status").
	SiteKey string

	// --- Mode B auth (mirrors the home/fin pattern) ---
	AuthBaseURL       string // shared auth base URL, e.g. "https://auth.tilcer.cz"
	AuthServiceSecret string // X-Service-Secret for /internal/*; never logged
	AuthJWTSecret     string // shared HS256 secret to VERIFY returned tokens; never logged
	AuthJWTIssuer     string // expected token `iss`; "" = do not enforce

	AllowedOrigins     []string // CSRF Origin allowlist
	SessionTTLDays     int      // session sliding window (default 90)
	RoleRefreshMinutes int      // re-mint interval for cached roles (default 15)

	// TrustedProxyCount is how many X-Forwarded-For-appending reverse proxies sit
	// in front of this service. It governs client-IP resolution for access logs
	// and login rate-limit keying: 0 trusts no XFF and keys on the direct peer;
	// 1 (default) is Coolify's lone Traefik; use 2 when a CDN (e.g. Cloudflare)
	// fronts Traefik. Setting it too high lets clients spoof their rate-limit key.
	TrustedProxyCount int

	DevAuthBypass bool
	DevActorID    string
	DevActorRoles []string

	// --- Poller (net-new; PRD FR-5) ---
	CheckInterval   time.Duration // STATUS_CHECK_INTERVAL (default 5m)
	CheckTimeout    time.Duration // STATUS_CHECK_TIMEOUT (default 10s)
	PollConcurrency int           // bounded concurrency per poll cycle (default 8)

	// --- Color (PRD FR-6) ---
	RedFailThreshold int // consecutive failed checks before red (default 2)

	// --- Uptime / rollups (PRD FR-13) ---
	UptimeWindowDays int // rolling window for cached uptime_pct (default 90)

	// --- Retention (PRD FR-10) ---
	RetentionDays       int // raw check_result / crash_event purge window (default 90)
	RollupRetentionDays int // check_rollup retention (default 400)

	// --- Crash ingest guards (PRD FR-7) ---
	MaxIngestBytes     int64   // body cap (default 65536)
	IngestRatePerSec   float64 // token refill rate per second (default 1.0 == 60/min)
	IngestBurst        int     // token bucket burst (default 120)
	ReopenOnRegression bool    // reopen resolved groups on new events (default true)

	// --- Scheduler daily job time (UTC) ---
	DailyAtHour int // STATUS_DAILY_JOB_AT hour (default 0)
	DailyAtMin  int // STATUS_DAILY_JOB_AT minute (default 15)
}

// IsProduction reports whether the service is running in production.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// Redacted returns a log-safe one-line summary of the configuration with secrets masked.
func (c *Config) Redacted() string {
	mask := func(s string) string {
		if s == "" {
			return "unset"
		}
		return "set(***)"
	}
	static := c.StaticDir
	if static == "" {
		static = "none"
	}
	jwtIssuer := c.AuthJWTIssuer
	if jwtIssuer == "" {
		jwtIssuer = "any"
	}
	return fmt.Sprintf(
		"env=%s addr=%s db=%s static=%s site=%s auth_base=%s auth_secret=%s jwt_secret=%s jwt_issuer=%s "+
			"origins=%v session_ttl_days=%d role_refresh_min=%d trusted_proxies=%d dev_auth_bypass=%t "+
			"check_interval=%s check_timeout=%s poll_concurrency=%d red_fail_threshold=%d uptime_window_days=%d "+
			"retention_days=%d rollup_retention_days=%d max_ingest_bytes=%d ingest_rate_per_sec=%.4f ingest_burst=%d "+
			"reopen_on_regression=%t daily_job_at=%02d:%02d",
		c.Env, c.Addr, c.DBPath, static, c.SiteKey, c.AuthBaseURL, mask(c.AuthServiceSecret), mask(c.AuthJWTSecret), jwtIssuer,
		c.AllowedOrigins, c.SessionTTLDays, c.RoleRefreshMinutes, c.TrustedProxyCount, c.DevAuthBypass,
		c.CheckInterval, c.CheckTimeout, c.PollConcurrency, c.RedFailThreshold, c.UptimeWindowDays,
		c.RetentionDays, c.RollupRetentionDays, c.MaxIngestBytes, c.IngestRatePerSec, c.IngestBurst,
		c.ReopenOnRegression, c.DailyAtHour, c.DailyAtMin,
	)
}

// Getenv is the environment lookup used by Load; it mirrors os.LookupEnv and is
// injected in tests.
type Getenv func(key string) (string, bool)

// Defaults for optional variables.
const (
	defaultEnv                = "development"
	defaultAddr               = ":112"
	defaultSiteKey            = "status"
	defaultSessionTTLDays     = 90
	defaultRoleRefreshMinutes = 15
	defaultTrustedProxyCount  = 1 // Coolify's lone Traefik appends the real client
)

var defaultAllowedOrigins = []string{"https://*.tilcer.cz"}

// LoadFromEnv loads the configuration using os.LookupEnv.
func LoadFromEnv() (*Config, error) {
	return Load(osLookup)
}

// Load reads, defaults, and validates configuration from getenv. On any problem
// it returns a single error enumerating every issue found (not just the first).
func Load(getenv Getenv) (*Config, error) {
	l := &loader{getenv: getenv}
	c := &Config{}

	c.Env = l.strDefault("STATUS_ENV", defaultEnv)
	if c.Env != "development" && c.Env != "production" {
		l.errf("STATUS_ENV must be \"development\" or \"production\" (got %q)", c.Env)
	}
	c.Addr = l.strDefault("STATUS_ADDR", defaultAddr)
	c.DBPath = l.strRequired("STATUS_DB_PATH")
	c.StaticDir = l.strDefault("STATUS_STATIC_DIR", "")
	c.SiteKey = l.strDefault("STATUS_SITE_KEY", defaultSiteKey)

	c.DevAuthBypass = l.boolDefault("STATUS_DEV_AUTH_BYPASS", false)
	c.DevActorID = l.strDefault("STATUS_DEV_ACTOR_ID", "dev-user")
	c.DevActorRoles = l.csvDefault("STATUS_DEV_ACTOR_ROLES", []string{"admin"})

	// The auth service is only strictly required when the bypass is off.
	if c.DevAuthBypass {
		c.AuthBaseURL = l.strDefault("AUTH_BASE_URL", "")
		c.AuthServiceSecret = l.strDefault("STATUS_AUTH_SERVICE_SECRET", "")
		c.AuthJWTSecret = l.strDefault("STATUS_AUTH_JWT_SECRET", "")
	} else {
		c.AuthBaseURL = l.strRequired("AUTH_BASE_URL")
		c.AuthServiceSecret = l.strRequired("STATUS_AUTH_SERVICE_SECRET")
		c.AuthJWTSecret = l.strRequired("STATUS_AUTH_JWT_SECRET")
	}
	c.AuthJWTIssuer = l.strDefault("STATUS_AUTH_JWT_ISSUER", "")

	c.AllowedOrigins = l.csvDefault("STATUS_ALLOWED_ORIGINS", defaultAllowedOrigins)
	c.SessionTTLDays = l.intDefault("STATUS_SESSION_TTL_DAYS", defaultSessionTTLDays)
	c.RoleRefreshMinutes = l.intDefault("STATUS_ROLE_REFRESH_MINUTES", defaultRoleRefreshMinutes)
	c.TrustedProxyCount = l.intDefault("STATUS_TRUSTED_PROXY_COUNT", defaultTrustedProxyCount)

	// Poller.
	c.CheckInterval = l.durationDefault("STATUS_CHECK_INTERVAL", 5*time.Minute)
	c.CheckTimeout = l.durationDefault("STATUS_CHECK_TIMEOUT", 10*time.Second)
	c.PollConcurrency = l.intDefault("STATUS_POLL_CONCURRENCY", 8)

	// Color.
	c.RedFailThreshold = l.intDefault("STATUS_RED_FAIL_THRESHOLD", 2)

	// Uptime / rollups.
	c.UptimeWindowDays = l.daysDefault("STATUS_UPTIME_WINDOW", 90)

	// Retention.
	c.RetentionDays = l.intDefault("STATUS_RETENTION_DAYS", 90)
	c.RollupRetentionDays = l.intDefault("STATUS_ROLLUP_RETENTION_DAYS", 400)

	// Ingest guards.
	c.MaxIngestBytes = int64(l.intDefault("STATUS_MAX_INGEST_BYTES", 65536))
	c.IngestRatePerSec = l.rateDefault("STATUS_INGEST_RATE", 1.0) // 60/min
	c.IngestBurst = l.intDefault("STATUS_INGEST_BURST", 120)
	c.ReopenOnRegression = l.boolDefault("STATUS_REOPEN_ON_REGRESSION", true)

	// Scheduler daily job time.
	c.DailyAtHour, c.DailyAtMin = l.hhmmDefault("STATUS_DAILY_JOB_AT", 0, 15)

	// Range sanity.
	if c.SessionTTLDays < 1 {
		l.errf("STATUS_SESSION_TTL_DAYS must be >= 1 (got %d)", c.SessionTTLDays)
	}
	if c.RoleRefreshMinutes < 1 {
		l.errf("STATUS_ROLE_REFRESH_MINUTES must be >= 1 (got %d)", c.RoleRefreshMinutes)
	}
	if c.TrustedProxyCount < 0 {
		l.errf("STATUS_TRUSTED_PROXY_COUNT must be >= 0 (got %d)", c.TrustedProxyCount)
	}
	if c.CheckInterval <= 0 {
		l.errf("STATUS_CHECK_INTERVAL must be > 0 (got %s)", c.CheckInterval)
	}
	if c.CheckTimeout <= 0 {
		l.errf("STATUS_CHECK_TIMEOUT must be > 0 (got %s)", c.CheckTimeout)
	}
	if c.PollConcurrency < 1 {
		l.errf("STATUS_POLL_CONCURRENCY must be >= 1 (got %d)", c.PollConcurrency)
	}
	if c.RedFailThreshold < 1 {
		l.errf("STATUS_RED_FAIL_THRESHOLD must be >= 1 (got %d)", c.RedFailThreshold)
	}
	if c.UptimeWindowDays < 1 {
		l.errf("STATUS_UPTIME_WINDOW must be >= 1 day (got %d)", c.UptimeWindowDays)
	}
	if c.RetentionDays < 1 {
		l.errf("STATUS_RETENTION_DAYS must be >= 1 (got %d)", c.RetentionDays)
	}
	if c.RollupRetentionDays < c.RetentionDays {
		l.errf("STATUS_ROLLUP_RETENTION_DAYS (%d) must be >= STATUS_RETENTION_DAYS (%d)", c.RollupRetentionDays, c.RetentionDays)
	}
	if c.RollupRetentionDays < c.UptimeWindowDays {
		// The daily purge deletes check_rollup rows older than RollupRetentionDays,
		// but the cached uptime_pct and the /uptime endpoint read rollups back
		// UptimeWindowDays. A shorter rollup retention would silently truncate the
		// reported uptime window with no error, so require it to cover the window.
		l.errf("STATUS_ROLLUP_RETENTION_DAYS (%d) must be >= STATUS_UPTIME_WINDOW (%d) so the uptime window's rollups are not purged before they are read", c.RollupRetentionDays, c.UptimeWindowDays)
	}
	if c.MaxIngestBytes < 1 {
		l.errf("STATUS_MAX_INGEST_BYTES must be >= 1 (got %d)", c.MaxIngestBytes)
	}
	if c.IngestRatePerSec <= 0 {
		l.errf("STATUS_INGEST_RATE must be > 0 (got %.4f/s)", c.IngestRatePerSec)
	}
	if c.IngestBurst < 1 {
		l.errf("STATUS_INGEST_BURST must be >= 1 (got %d)", c.IngestBurst)
	}

	// Security hard-stop: the dev bypass must never be active in production.
	if c.DevAuthBypass && c.IsProduction() {
		l.errf("STATUS_DEV_AUTH_BYPASS must not be enabled when STATUS_ENV=production " +
			"(fake authentication in production is a security hole)")
	}

	if len(l.errs) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(l.errs, "\n  - "))
	}
	return c, nil
}

// osLookup adapts os.LookupEnv to the Getenv signature.
func osLookup(key string) (string, bool) { return os.LookupEnv(key) }

// loader accumulates validation errors while reading typed values.
type loader struct {
	getenv Getenv
	errs   []string
}

func (l *loader) errf(format string, a ...any) { l.errs = append(l.errs, fmt.Sprintf(format, a...)) }

func (l *loader) strRequired(key string) string {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		l.errf("%s is required", key)
		return ""
	}
	return v
}

func (l *loader) strDefault(key, def string) string {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func (l *loader) intDefault(key string, def int) int {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		l.errf("%s must be an integer (got %q)", key, v)
		return def
	}
	return n
}

func (l *loader) boolDefault(key string, def bool) bool {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		l.errf("%s must be a boolean (got %q)", key, v)
		return def
	}
	return b
}

// csvDefault splits a comma-separated value, trimming whitespace and dropping
// empty entries. Returns def when the variable is unset or empty.
func (l *loader) csvDefault(key string, def []string) []string {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// durationDefault parses a Go duration string ("5m", "10s"). Returns def when
// unset/empty; records an error on a bad value.
func (l *loader) durationDefault(key string, def time.Duration) time.Duration {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		l.errf("%s must be a duration like \"5m\" (got %q)", key, v)
		return def
	}
	return d
}

// daysDefault parses a day count, accepting either "90d" or a plain integer "90".
func (l *loader) daysDefault(key string, def int) int {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	s := strings.TrimSpace(v)
	s = strings.TrimSuffix(s, "d")
	n, err := strconv.Atoi(s)
	if err != nil {
		l.errf("%s must be a day count like \"90d\" or \"90\" (got %q)", key, v)
		return def
	}
	return n
}

// rateDefault parses a rate expressed as "N/min", "N/s", or "N/sec" into a
// per-second float. A bare number is treated as per-second.
func (l *loader) rateDefault(key string, def float64) float64 {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	s := strings.TrimSpace(v)
	num, unit, hasUnit := strings.Cut(s, "/")
	n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		l.errf("%s must be a rate like \"60/min\" or \"2/s\" (got %q)", key, v)
		return def
	}
	if !hasUnit {
		return n // per second
	}
	switch strings.TrimSpace(strings.ToLower(unit)) {
	case "s", "sec", "second":
		return n
	case "min", "m", "minute":
		return n / 60.0
	case "h", "hour":
		return n / 3600.0
	default:
		l.errf("%s has an unknown rate unit %q (use /s, /min, or /h)", key, unit)
		return def
	}
}

// hhmmDefault parses "HH:MM" (UTC) into hour and minute. Returns the defaults on
// unset/empty; records an error on a bad value.
func (l *loader) hhmmDefault(key string, dh, dm int) (int, int) {
	v, ok := l.getenv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return dh, dm
	}
	hh, mm, found := strings.Cut(strings.TrimSpace(v), ":")
	if !found {
		l.errf("%s must be \"HH:MM\" (got %q)", key, v)
		return dh, dm
	}
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		l.errf("%s must be a valid 24h time \"HH:MM\" (got %q)", key, v)
		return dh, dm
	}
	return h, m
}
