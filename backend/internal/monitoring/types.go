package monitoring

// CheckResult is one reachability check (openapi CheckResult).
type CheckResult struct {
	ID         int64   `json:"id"`
	SiteID     string  `json:"site_id"`
	CheckedAt  string  `json:"checked_at"`
	OK         bool    `json:"ok"`
	StatusCode *int    `json:"status_code"`
	LatencyMs  *int    `json:"latency_ms"`
	Error      *string `json:"error"`
}

// CheckPage is the paginated check-history response.
type CheckPage struct {
	Items      []CheckResult `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

// UptimeBucket is one time bucket of the uptime strip (openapi UptimeBucket).
// OkPct is null for a bucket with no checks (a gap, distinct from 0%).
type UptimeBucket struct {
	Start        string   `json:"start"`
	End          string   `json:"end"`
	OkPct        *float64 `json:"ok_pct"`
	Checks       int      `json:"checks"`
	Failed       int      `json:"failed"`
	LatencyP50Ms *int     `json:"latency_p50_ms"`
}

// UptimeSummary is the aggregated uptime/latency response (openapi UptimeSummary).
type UptimeSummary struct {
	Window       string         `json:"window"`
	From         string         `json:"from"`
	To           string         `json:"to"`
	UptimePct    *float64       `json:"uptime_pct"`
	ChecksTotal  int            `json:"checks_total"`
	ChecksFailed int            `json:"checks_failed"`
	LatencyP50Ms *int           `json:"latency_p50_ms"`
	LatencyP95Ms *int           `json:"latency_p95_ms"`
	Buckets      []UptimeBucket `json:"buckets"`
}

// monitoredSite is a poll target.
type monitoredSite struct {
	ID             string
	URL            string
	ExpectedStatus int
}

// checkOutcome is one poll result before it is written.
type checkOutcome struct {
	SiteID     string
	OK         bool
	StatusCode *int
	LatencyMs  *int
	Err        string
}
