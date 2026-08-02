package sites

// SiteSummary is the board/detail wire shape (openapi SiteSummary). color is
// computed on read; uptime_pct is the cached rolling figure (null when
// monitoring is off or the site has never been checked).
type SiteSummary struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Color            Color    `json:"color"`
	MonitorURL       *string  `json:"monitor_url"`
	MonitorEnabled   bool     `json:"monitor_enabled"`
	ExpectedStatus   int      `json:"expected_status"`
	CrashWindowHours int      `json:"crash_window_hours"`
	LastCheckedAt    *string  `json:"last_checked_at"`
	LastOK           *bool    `json:"last_ok"`
	FailStreak       int      `json:"fail_streak"`
	UptimePct        *float64 `json:"uptime_pct"`
	OpenCrashGroups  int      `json:"open_crash_groups"`
	RecentCrashCount int      `json:"recent_crash_count"`
	CreatedAt        string   `json:"created_at"`
}

// SiteWithKey is the create/rotate response: a summary plus the plaintext ingest
// key, shown exactly once (openapi SiteWithKey).
type SiteWithKey struct {
	SiteSummary
	IngestKey string `json:"ingest_key"`
}

// createParams is the validated, defaulted input to Store.Create.
type createParams struct {
	ID               string
	Name             string
	MonitorURL       string // "" = crash-only
	MonitorEnabled   bool   // already coerced to false when MonitorURL == ""
	ExpectedStatus   int
	CrashWindowHours int
	IngestKeyHash    string
}

// updateParams carries the fields a PATCH actually provided. The *Set booleans
// distinguish "absent" (leave unchanged) from a provided value — notably
// MonitorURL, where a provided empty string clears the URL (crash-only).
type updateParams struct {
	Name              *string
	MonitorURLSet     bool
	MonitorURL        string
	MonitorEnabledSet bool
	MonitorEnabled    bool
	ExpectedStatus    *int
	CrashWindowHours  *int
}
