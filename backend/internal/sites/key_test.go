package sites

import (
	"strings"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	plaintext, hash, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !strings.HasPrefix(plaintext, "ik_") {
		t.Fatalf("key %q missing ik_ prefix", plaintext)
	}
	if len(plaintext) < 40 {
		t.Fatalf("key %q looks too short", plaintext)
	}
	if hash != HashKey(plaintext) {
		t.Fatalf("returned hash does not match HashKey(plaintext)")
	}
	if hash == plaintext {
		t.Fatalf("hash must not equal plaintext")
	}
	if len(hash) != 64 {
		t.Fatalf("sha-256 hex hash should be 64 chars, got %d", len(hash))
	}
}

func TestKeyUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		k, _, err := GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		if seen[k] {
			t.Fatalf("duplicate key generated: %q", k)
		}
		seen[k] = true
	}
}

func TestConstantTimeMatch(t *testing.T) {
	plaintext, hash, _ := GenerateKey()
	if !ConstantTimeMatch(plaintext, hash) {
		t.Fatalf("correct key should match its hash")
	}
	if ConstantTimeMatch("ik_wrongkey", hash) {
		t.Fatalf("wrong key must not match")
	}
	if ConstantTimeMatch(plaintext, HashKey("ik_other")) {
		t.Fatalf("key must not match a different hash")
	}
	if ConstantTimeMatch("", hash) {
		t.Fatalf("empty key must not match")
	}
}
