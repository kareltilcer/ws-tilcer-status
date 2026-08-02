package sites

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// keyPrefix marks a status ingest key (Sentry-DSN style, public by design).
const keyPrefix = "ik_"

// GenerateKey mints a new ingest key: "ik_" + 32 random url-safe bytes. It
// returns the plaintext (shown to the admin exactly once) and its SHA-256 hex
// hash (the only thing persisted). The key is 256-bit high-entropy, so a fast
// hash + constant-time compare is sufficient — no slow KDF needed.
func GenerateKey() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("sites: generate ingest key: %w", err)
	}
	plaintext = keyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, HashKey(plaintext), nil
}

// HashKey returns the SHA-256 hex digest of an ingest key.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeMatch reports whether plaintext hashes to storedHash, comparing in
// constant time to avoid leaking a match via timing.
func ConstantTimeMatch(plaintext, storedHash string) bool {
	got := HashKey(plaintext)
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}
