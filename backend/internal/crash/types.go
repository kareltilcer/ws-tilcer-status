package crash

// Config carries the ingest guards and grouping policy from platform config.
type Config struct {
	MaxIngestBytes     int64
	IngestRatePerSec   float64
	IngestBurst        int
	RedFailThreshold   int
	ReopenOnRegression bool
}

// CrashReport is the public ingest request body (openapi CrashReport).
type CrashReport struct {
	Message     string         `json:"message"`
	Level       string         `json:"level"`
	Stack       string         `json:"stack"`
	Environment string         `json:"environment"`
	Release     string         `json:"release"`
	Fingerprint string         `json:"fingerprint"`
	Context     map[string]any `json:"context"`
	OccurredAt  string         `json:"occurred_at"`
}

// ingestResp is the 202 body.
type ingestResp struct {
	GroupID int64 `json:"group_id"`
	EventID int64 `json:"event_id"`
}

// CrashGroup is the wire shape for a fingerprint group (openapi CrashGroup).
type CrashGroup struct {
	ID          int64  `json:"id"`
	SiteID      string `json:"site_id"`
	Fingerprint string `json:"fingerprint"`
	Title       string `json:"title"`
	Level       string `json:"level"`
	Count       int    `json:"count"`
	Status      string `json:"status"`
	FirstSeen   string `json:"first_seen"`
	LastSeen    string `json:"last_seen"`
}

// CrashEvent is the wire shape for one event (openapi CrashEvent).
type CrashEvent struct {
	ID          int64          `json:"id"`
	GroupID     int64          `json:"group_id"`
	SiteID      string         `json:"site_id"`
	Level       string         `json:"level"`
	Message     string         `json:"message"`
	Stack       *string        `json:"stack"`
	Environment *string        `json:"environment"`
	Release     *string        `json:"release"`
	Context     map[string]any `json:"context"`
	OccurredAt  string         `json:"occurred_at"`
	ReceivedAt  string         `json:"received_at"`
}

// GroupPage is the list-groups response.
type GroupPage struct {
	Items      []CrashGroup `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

// GroupDetail is the get-group response (group + paginated events).
type GroupDetail struct {
	Group      CrashGroup   `json:"group"`
	Events     []CrashEvent `json:"events"`
	NextCursor *string      `json:"next_cursor"`
}

// levels / statuses.
const (
	LevelFatal   = "fatal"
	LevelError   = "error"
	LevelWarning = "warning"

	StatusOpen     = "open"
	StatusResolved = "resolved"
	StatusIgnored  = "ignored"
)

func validLevel(l string) bool {
	switch l {
	case LevelFatal, LevelError, LevelWarning:
		return true
	}
	return false
}

func validStatus(s string) bool {
	switch s {
	case StatusOpen, StatusResolved, StatusIgnored:
		return true
	}
	return false
}

// levelRank ranks severity so a group tracks the highest level seen.
func levelRank(l string) int {
	switch l {
	case LevelFatal:
		return 3
	case LevelError:
		return 2
	case LevelWarning:
		return 1
	}
	return 0
}

func maxLevel(a, b string) string {
	if levelRank(b) > levelRank(a) {
		return b
	}
	return a
}
