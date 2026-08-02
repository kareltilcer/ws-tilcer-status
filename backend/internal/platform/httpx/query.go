package httpx

import "strconv"

// AtoiDefault parses s as a base-10 int, returning def when s is empty or not a
// valid integer. It is the shared parser for optional integer query params (e.g.
// ?limit=), so every list endpoint treats a missing or malformed value the same.
func AtoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
