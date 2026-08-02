package config

import (
	"strings"
	"testing"
)

// envGetter builds a Getenv over a fixed map.
func envGetter(env map[string]string) Getenv {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

// TestRollupRetentionMustCoverUptimeWindow guards the invariant that the rollup
// retention window is at least as long as the uptime window — otherwise the daily
// purge deletes rollups the uptime figure still reads, silently truncating it.
func TestRollupRetentionMustCoverUptimeWindow(t *testing.T) {
	env := map[string]string{
		"STATUS_DB_PATH":               "/tmp/status.db",
		"STATUS_DEV_AUTH_BYPASS":       "true",
		"STATUS_RETENTION_DAYS":        "90",
		"STATUS_UPTIME_WINDOW":         "200",
		"STATUS_ROLLUP_RETENTION_DAYS": "120", // >= RetentionDays but < UptimeWindow
	}

	_, err := Load(envGetter(env))
	if err == nil {
		t.Fatal("expected an error when rollup retention (120) < uptime window (200)")
	}
	if !strings.Contains(err.Error(), "STATUS_ROLLUP_RETENTION_DAYS") || !strings.Contains(err.Error(), "STATUS_UPTIME_WINDOW") {
		t.Fatalf("error should name both offending vars, got: %v", err)
	}

	// Widening rollup retention to cover the uptime window passes validation.
	env["STATUS_ROLLUP_RETENTION_DAYS"] = "200"
	if _, err := Load(envGetter(env)); err != nil {
		t.Fatalf("rollup retention == uptime window should be valid, got: %v", err)
	}
}
