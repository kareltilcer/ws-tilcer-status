package feedback

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// resolvedFile is one declared file after validation: the allow-list entry whose
// content type will be signed, and the size the upload URL will be signed for.
type resolvedFile struct {
	Type       acceptedType
	SignedSize int64
}

// resolveFiles validates the declared files against the allow-list and the caps.
//
// ⚠ The size is CLAMPED to the cap before it is signed (V3-D08), and the clamp is
// the enforcement: a URL signed for N bytes refuses a PUT declaring anything else,
// and it refuses it at R2 before a byte reaches the droplet. A client that
// over-declares therefore receives a URL that will not accept the file it holds —
// which is the intended outcome, not a failure to handle.
func (m *Module) resolveFiles(files []DeclaredFile) ([]resolvedFile, error) {
	if len(files) > m.cfg.MaxFiles {
		return nil, fmt.Errorf("at most %d files may be attached", m.cfg.MaxFiles)
	}
	out := make([]resolvedFile, 0, len(files))
	for _, f := range files {
		t, ok := lookupType(f.ContentType)
		if !ok {
			return nil, fmt.Errorf("content_type %q is not accepted", f.ContentType)
		}
		if f.ByteSize < 1 {
			return nil, fmt.Errorf("byte_size must be at least 1 (got %d)", f.ByteSize)
		}
		limit := m.cfg.MaxImageBytes
		if t.video {
			limit = m.cfg.MaxVideoBytes
		}
		size := f.ByteSize
		if size > limit {
			size = limit
		}
		out = append(out, resolvedFile{Type: t, SignedSize: size})
	}
	return out, nil
}

// objectKey builds feedback/{site_id}/{ref}/{n}-{rand}.{ext}. The extension comes
// from the allow-listed content type, never from a client-supplied filename: the
// filename is user input and this is a path.
func objectKey(siteID, ref string, n int, ext string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("feedback: object key entropy: %w", err)
	}
	return fmt.Sprintf("%s%s/%s/%d-%s.%s", objectPrefix, siteID, ref, n, hex.EncodeToString(b), ext), nil
}
