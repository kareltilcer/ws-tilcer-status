package feedback

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// widgetKeyPrefix marks a widget key. It is rendered into the host app's HTML,
// so it is public by design — see PRD §V3-3 on why the key is not a boundary.
const widgetKeyPrefix = "wk_"

// GenerateWidgetKey mints "wk_" + 32 random url-safe bytes and returns the
// plaintext (shown to the admin exactly once) with its SHA-256 hex hash (the only
// thing persisted). Hashing and comparison are the registry's — one place, so the
// widget key cannot end up compared differently from the ingest key.
//
// ⚠ The two keys are deliberately separate: rotating a spammed widget key must
// not silence that site's crash reporting.
func GenerateWidgetKey() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("feedback: generate widget key: %w", err)
	}
	plaintext = widgetKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, sites.HashKey(plaintext), nil
}
