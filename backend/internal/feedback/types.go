package feedback

// Wire shapes for openapi 0.3.0's feedback and widget tags. Field names and
// nullability follow the contract exactly.

// Report kinds (openapi ReportKind). A hint from the reporter, not a verdict —
// triage may change it.
const (
	KindBug   = "bug"
	KindIdea  = "idea"
	KindOther = "other"
)

// Triage states (openapi ReportState): new → open → resolved | declined.
const (
	StateNew      = "new"
	StateOpen     = "open"
	StateResolved = "resolved"
	StateDeclined = "declined"
)

// Attachment states (openapi AttachmentState).
const (
	AttachPending = "pending" // a slot was issued; the object is unconfirmed
	AttachStored  = "stored"  // HEAD confirmed it at the expected size
	AttachMissing = "missing" // never uploaded, wrong size, or swept
)

// kinds is the set the widget offers and the API accepts.
var kinds = []string{KindBug, KindIdea, KindOther}

func validKind(s string) bool { return s == KindBug || s == KindIdea || s == KindOther }
func validState(s string) bool {
	return s == StateNew || s == StateOpen || s == StateResolved || s == StateDeclined
}

// terminalState reports whether a state stamps resolved_at. Moving back off one
// clears it, so a reopened report does not carry a resolution date.
func terminalState(s string) bool { return s == StateResolved || s == StateDeclined }

// AttachmentSummary is one attachment as the dashboard and the claim response see
// it (openapi AttachmentSummary).
type AttachmentSummary struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	ContentType string `json:"content_type"`
	// ByteSize is what R2 reported at claim; null until then. The declared size
	// is never published as if it were confirmed.
	ByteSize  *int64 `json:"byte_size"`
	CreatedAt string `json:"created_at"`
}

// ReportSummary is one row of the inbox (openapi ReportSummary).
type ReportSummary struct {
	Ref             string  `json:"ref"`
	SiteID          string  `json:"site_id"`
	Kind            string  `json:"kind"`
	State           string  `json:"state"`
	Message         string  `json:"message"`
	ReporterLabel   *string `json:"reporter_label"`
	AttachmentCount int     `json:"attachment_count"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// Report is one report in full (openapi Report).
type Report struct {
	ReportSummary
	PageURL      *string             `json:"page_url"`
	Referrer     *string             `json:"referrer"`
	UserAgent    *string             `json:"user_agent"`
	Viewport     *string             `json:"viewport"`
	Locale       *string             `json:"locale"`
	AppRelease   *string             `json:"app_release"`
	ConsoleTail  []string            `json:"console_tail"`
	LastError    *string             `json:"last_error"`
	InternalNote *string             `json:"internal_note"`
	ResolvedAt   *string             `json:"resolved_at"`
	Attachments  []AttachmentSummary `json:"attachments"`
}

// ReportPage is the inbox response.
type ReportPage struct {
	Items      []ReportSummary `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

// FeedbackConfig is a site's feedback configuration (openapi FeedbackConfig). A
// site with no row reads as disabled — absence is the default state, not a
// missing row to repair.
type FeedbackConfig struct {
	SiteID         string  `json:"site_id"`
	Enabled        bool    `json:"enabled"`
	ConsoleCapture bool    `json:"console_capture"`
	WidgetKeySetAt *string `json:"widget_key_set_at"`
	UpdatedAt      *string `json:"updated_at"`
}

// FeedbackConfigWithKey is the PATCH response on a first enable: the config plus
// the plaintext widget key, shown exactly once.
type FeedbackConfigWithKey struct {
	FeedbackConfig
	WidgetKey string `json:"widget_key,omitempty"`
}

// WidgetConfig is what the widget needs before it renders anything (openapi
// WidgetConfig). Everything but `enabled` is omitted when feedback is off: a
// disabled site's answer must not read like a configured one.
type WidgetConfig struct {
	Enabled        bool     `json:"enabled"`
	Ticket         *string  `json:"ticket,omitempty"`
	Kinds          []string `json:"kinds,omitempty"`
	MaxFiles       int      `json:"max_files,omitempty"`
	MaxImageBytes  int64    `json:"max_image_bytes,omitempty"`
	MaxVideoBytes  int64    `json:"max_video_bytes,omitempty"`
	Accept         []string `json:"accept,omitempty"`
	ConsoleCapture bool     `json:"console_capture,omitempty"`
	StringsVersion int      `json:"strings_version,omitempty"`
}

// DeclaredFile is one file the client says it is about to upload (openapi
// DeclaredFile). Both fields are declarations: the size is clamped to the cap
// before the URL is signed, and the content type must be on the allow-list.
type DeclaredFile struct {
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
}

// Submission is the widget's report payload (openapi FeedbackSubmission).
type Submission struct {
	Message       string   `json:"message"`
	Kind          string   `json:"kind"`
	Ticket        string   `json:"ticket"`
	ReporterLabel *string  `json:"reporter_label"`
	PageURL       *string  `json:"page_url"`
	Referrer      *string  `json:"referrer"`
	Viewport      *string  `json:"viewport"`
	Locale        *string  `json:"locale"`
	AppRelease    *string  `json:"app_release"`
	ConsoleTail   []string `json:"console_tail"`
	LastError     *string  `json:"last_error"`
	// Website is the honeypot: a hidden field no human fills in, named plausibly
	// on purpose. Non-empty is a 422.
	Website *string        `json:"website"`
	Files   []DeclaredFile `json:"files"`
}

// UploadSlot is one presigned PUT (openapi UploadSlot).
type UploadSlot struct {
	AttachmentID int64             `json:"attachment_id"`
	URL          string            `json:"url"`
	ExpiresAt    string            `json:"expires_at"`
	Headers      map[string]string `json:"headers"`
}

// Accepted is the 202 body (openapi FeedbackAccepted). Upload URLs exist ONLY
// here: there is no standalone "give me an upload URL" endpoint, so no accepted
// report means no way to write a byte into the bucket.
type Accepted struct {
	Ref     string       `json:"ref"`
	Uploads []UploadSlot `json:"uploads"`
}

// ClaimResult is the claim response.
type ClaimResult struct {
	Attachments []AttachmentSummary `json:"attachments"`
}

// AttachmentURL is the presigned view response.
type AttachmentURL struct {
	URL         string `json:"url"`
	ExpiresAt   string `json:"expires_at"`
	ContentType string `json:"content_type"`
}
