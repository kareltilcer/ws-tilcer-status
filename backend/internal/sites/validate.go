package sites

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// siteIDRe is the user-supplied site id format (openapi SiteIdString).
var siteIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidID reports whether id matches the required slug format.
func ValidID(id string) bool { return siteIDRe.MatchString(id) }

// validateName trims and requires a non-empty display name.
func validateName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", fmt.Errorf("name is required")
	}
	if len(n) > 200 {
		return "", fmt.Errorf("name is too long (max 200)")
	}
	return n, nil
}

// validateMonitorURL validates an optional monitor URL: it must be an absolute
// http(s) URL with a host. An empty/nil value means crash-only.
func validateMonitorURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("monitor_url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("monitor_url must be http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("monitor_url must include a host")
	}
	return s, nil
}

// validateExpectedStatus bounds the expected HTTP status to a real code.
func validateExpectedStatus(code int) error {
	if code < 100 || code > 599 {
		return fmt.Errorf("expected_status must be a valid HTTP status (100-599)")
	}
	return nil
}

// validateCrashWindow requires a positive window.
func validateCrashWindow(hours int) error {
	if hours < 1 {
		return fmt.Errorf("crash_window_hours must be >= 1")
	}
	return nil
}
