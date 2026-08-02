// Package timeutil pins the one timestamp format the service stores and compares.
// Every persisted timestamp is RFC3339 UTC with a FIXED-WIDTH nanosecond fraction
// so that string comparison is a valid chronological order — the property keyset
// pagination cursors and windowed range queries rely on. (Go's default
// RFC3339Nano trims trailing zeros, which breaks lexical ordering; this layout
// does not.)
package timeutil

import "time"

// Layout is the canonical stored timestamp layout (fixed width, UTC → "Z").
const Layout = "2006-01-02T15:04:05.000000000Z07:00"

// Format renders t in the canonical layout (UTC).
func Format(t time.Time) string { return t.UTC().Format(Layout) }

// Parse accepts the canonical layout or any RFC3339 timestamp (client-supplied
// occurred_at may be plain RFC3339) and returns it in UTC.
func Parse(s string) (time.Time, error) {
	if t, err := time.Parse(Layout, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	return t.UTC(), err
}

// Day returns the UTC calendar date (YYYY-MM-DD) of t — the rollup partition key.
func Day(t time.Time) string { return t.UTC().Format("2006-01-02") }
