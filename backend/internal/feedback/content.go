package feedback

import "strings"

// acceptedType is one allow-listed attachment type and the extension the object
// key gets. ⚠ The extension is derived from the CONTENT TYPE, never from the
// client's filename (V3-D09): the filename is user input and the key is a path.
type acceptedType struct {
	contentType string
	ext         string
	video       bool
}

// acceptedTypes is the fixed allow-list, in the order the widget offers it. It is
// matched against what the client declares and then SIGNED into the upload URL,
// so R2 refuses a PUT whose type differs (measured: 403 SignatureDoesNotMatch) —
// the list is a real constraint at the bucket, not advice.
var acceptedTypes = []acceptedType{
	{"image/png", "png", false},
	{"image/jpeg", "jpg", false},
	{"image/webp", "webp", false},
	{"image/gif", "gif", false},
	{"video/mp4", "mp4", true},
	{"video/webm", "webm", true},
}

// lookupType returns the allow-list entry for a declared content type. Parameters
// ("image/png; charset=binary") are ignored and the type is matched
// case-insensitively, as media types are case-insensitive; anything else is a 422.
func lookupType(declared string) (acceptedType, bool) {
	base, _, _ := strings.Cut(declared, ";")
	base = strings.ToLower(strings.TrimSpace(base))
	for _, t := range acceptedTypes {
		if t.contentType == base {
			return t, true
		}
	}
	return acceptedType{}, false
}

// acceptList is the allow-list as the widget config publishes it.
func acceptList() []string {
	out := make([]string, 0, len(acceptedTypes))
	for _, t := range acceptedTypes {
		out = append(out, t.contentType)
	}
	return out
}
