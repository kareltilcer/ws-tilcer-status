package notify

// Event kinds — what a notify_event row is about.
const (
	KindCrashNew        = "crash_new"
	KindCrashRegression = "crash_regression"
	KindFeedback        = "feedback"
	KindSiteDown        = "site_down"
	KindSiteRecovered   = "site_recovered"
)

// Digest states. A digest is pending until the provider accepts it (sent) or the
// worker gives up on it (failed) — refused permanently, expired past the
// idempotency window, or cancelled because notifications were switched off.
const (
	DigestPending = "pending"
	DigestSent    = "sent"
	DigestFailed  = "failed"
)

// payload is the JSON snapshot stored on a notify_event. It holds what the email
// will say and nothing more: this text leaves the service for a mail provider, so
// a crash is a title and a message excerpt (never a stack), and a report is its
// excerpt and ref (never the reporter's label, page, browser or console).
type payload struct {
	// crash_new / crash_regression
	GroupID     int64  `json:"group_id,omitempty"`
	Title       string `json:"title,omitempty"`
	Level       string `json:"level,omitempty"`
	Environment string `json:"environment,omitempty"`
	Release     string `json:"release,omitempty"`

	// feedback
	Ref         string `json:"ref,omitempty"`
	ReportKind  string `json:"report_kind,omitempty"`
	Attachments int    `json:"attachments,omitempty"`

	// crash + feedback
	Message string `json:"message,omitempty"`

	// site_down / site_recovered
	URL        string `json:"url,omitempty"`
	StatusCode *int   `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
	DownSince  string `json:"down_since,omitempty"`

	// At is when it happened (timeutil.Layout).
	At string `json:"at"`
}

// --- wire types (openapi NotificationSettings, …) ---------------------------

// EventToggles are the three per-kind switches. Crash covers new groups and
// regressions; Downtime covers down and back up.
type EventToggles struct {
	Crash    bool `json:"crash"`
	Feedback bool `json:"feedback"`
	Downtime bool `json:"downtime"`
}

// Settings is GET /api/notifications/settings.
type Settings struct {
	// Available is whether this deployment has a mail provider at all.
	Available bool `json:"available"`
	// Provider names it ("resend", or "log" in development); null when unavailable.
	Provider *string `json:"provider"`
	// From is STATUS_MAIL_FROM, shown so a sender-domain problem is diagnosable.
	From    string `json:"from"`
	Enabled bool   `json:"enabled"`
	// Recipients is null for a caller who is not an admin: the addresses are
	// personal data, and nothing a non-admin can do with the page needs them.
	Recipients          []string     `json:"recipients"`
	Events              EventToggles `json:"events"`
	MutedSites          []string     `json:"muted_sites"`
	DigestWindowSeconds int          `json:"digest_window_seconds"`
	MaxPerHour          int          `json:"max_per_hour"`
	UpdatedAt           *string      `json:"updated_at"`
}

// settingsUpdate is the PUT body. Every field is required — a pointer is how a
// missing one is told from a false one.
type settingsUpdate struct {
	Enabled    *bool           `json:"enabled"`
	Recipients *[]string       `json:"recipients"`
	Events     *eventTogglesIn `json:"events"`
}

type eventTogglesIn struct {
	Crash    *bool `json:"crash"`
	Feedback *bool `json:"feedback"`
	Downtime *bool `json:"downtime"`
}

// Delivery is one digest on GET /api/notifications/deliveries.
type Delivery struct {
	ID         int64    `json:"id"`
	CreatedAt  string   `json:"created_at"`
	State      string   `json:"state"`
	Subject    string   `json:"subject"`
	EventCount int      `json:"event_count"`
	Recipients []string `json:"recipients"` // null for a non-admin, as on Settings
	Attempts   int      `json:"attempts"`
	// NextAttemptAt is set only while the digest is still pending.
	NextAttemptAt *string `json:"next_attempt_at"`
	LastError     *string `json:"last_error"`
	SentAt        *string `json:"sent_at"`
}

// DeliveryPage is the deliveries response.
type DeliveryPage struct {
	Items []Delivery `json:"items"`
}

// TestResult is the POST /api/notifications/test response.
type TestResult struct {
	ProviderMessageID string   `json:"provider_message_id"`
	Recipients        []string `json:"recipients"`
}
