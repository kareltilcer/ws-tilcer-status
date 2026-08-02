package crash

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Fingerprint groups similar crashes. A client-supplied fingerprint is honored
// verbatim; otherwise it is the SHA-256 of the site id, level, the NORMALIZED
// message, and the first stack frame. Normalization strips volatile tokens
// (ids, addresses, numbers, paths) so "user 41 not found" and "user 99 not
// found" collapse to one group. This function defines grouping quality and is
// unit-tested in isolation.
func Fingerprint(siteID, level, message, stack, clientFP string) string {
	if strings.TrimSpace(clientFP) != "" {
		return clientFP
	}
	parts := strings.Join([]string{siteID, level, normalizeMessage(message), firstStackFrame(stack)}, "\x1f")
	sum := sha256.Sum256([]byte(parts))
	return hex.EncodeToString(sum[:])
}

// Ordered so broader patterns don't eat narrower ones (uuid/addr before generic
// hex; quoted strings and paths before the digit sweep).
var (
	reUUID     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reAddr     = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	reDQStr    = regexp.MustCompile(`"[^"]*"`)
	reSQStr    = regexp.MustCompile(`'[^']*'`)
	reWinPath  = regexp.MustCompile(`[A-Za-z]:\\[^\s"']+`)
	reUnixPath = regexp.MustCompile(`(?:/[\w.\-]+){2,}/?`)
	reLongHex  = regexp.MustCompile(`\b[0-9a-fA-F]{6,}\b`)
	reDigits   = regexp.MustCompile(`\d+`)
	reWS       = regexp.MustCompile(`\s+`)
)

// normalizeMessage replaces volatile substrings with stable placeholders and
// lowercases/whitespace-collapses the result.
func normalizeMessage(msg string) string {
	s := msg
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = reAddr.ReplaceAllString(s, "<addr>")
	s = reDQStr.ReplaceAllString(s, "<str>")
	s = reSQStr.ReplaceAllString(s, "<str>")
	s = reWinPath.ReplaceAllString(s, "<path>")
	s = reUnixPath.ReplaceAllString(s, "<path>")
	s = reLongHex.ReplaceAllString(s, "<hex>")
	s = reDigits.ReplaceAllString(s, "<n>")
	s = reWS.ReplaceAllString(s, " ")
	return strings.ToLower(strings.TrimSpace(s))
}

var (
	reOffset = regexp.MustCompile(`\+0x[0-9a-fA-F]+`)
	reLineNo = regexp.MustCompile(`:\d+`)
)

// firstStackFrame returns the first non-empty stack line with line/column numbers
// and hex offsets stripped, so main.go:42 and main.go:99 group together.
func firstStackFrame(stack string) string {
	for _, line := range strings.Split(stack, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		t = reOffset.ReplaceAllString(t, "")
		t = reLineNo.ReplaceAllString(t, "")
		return strings.TrimSpace(t)
	}
	return ""
}
