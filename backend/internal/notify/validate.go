package notify

import (
	"fmt"
	netmail "net/mail"
	"strings"
)

// maxRecipients bounds the list. They share one To: line, so everyone on it sees
// the others — this is a household list, not a mailing list.
const maxRecipients = 5

// maxAddressLen is RFC 5321's path limit.
const maxAddressLen = 254

// validateRecipients returns the cleaned list: blank entries skipped,
// duplicates dropped ignoring case, order kept.
//
// ⚠ Each entry must be a PLAIN address — no display name, no comments, nothing
// around it. It goes verbatim into a provider's `to` field, and anything
// net/mail would have to rewrite is either a typo or an attempt to smuggle a
// second header or recipient.
func validateRecipients(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if len(s) > maxAddressLen {
			return nil, fmt.Errorf("recipient %q is longer than %d characters", truncateRunes(s, 40), maxAddressLen)
		}
		a, err := netmail.ParseAddress(s)
		if err != nil || a.Name != "" || a.Address != s || !strings.Contains(s, "@") {
			return nil, fmt.Errorf("%q is not a plain email address", truncateRunes(s, 80))
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	if len(out) > maxRecipients {
		return nil, fmt.Errorf("at most %d recipients (got %d)", maxRecipients, len(out))
	}
	return out, nil
}
