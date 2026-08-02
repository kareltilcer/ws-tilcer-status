package crash

import "testing"

func TestFingerprintGrouping(t *testing.T) {
	// Messages differing only by volatile tokens must share a fingerprint.
	fpA := Fingerprint("fin", "error", "user 41 not found", "", "")
	fpB := Fingerprint("fin", "error", "user 99 not found", "", "")
	if fpA != fpB {
		t.Fatalf("expected same fingerprint for numeric-varying messages:\n  a=%s\n  b=%s", fpA, fpB)
	}

	// A genuinely different message opens a new group.
	fpC := Fingerprint("fin", "error", "database connection refused", "", "")
	if fpA == fpC {
		t.Fatalf("distinct messages must not collide")
	}

	// Different site → different group even for the same message.
	fpHome := Fingerprint("home", "error", "user 41 not found", "", "")
	if fpA == fpHome {
		t.Fatalf("same message on different sites must not collide")
	}

	// Different level → different group.
	fpWarn := Fingerprint("fin", "warning", "user 41 not found", "", "")
	if fpA == fpWarn {
		t.Fatalf("different levels must not collide")
	}
}

func TestFingerprintClientOverride(t *testing.T) {
	fp := Fingerprint("fin", "error", "anything", "stack", "my-custom-fp")
	if fp != "my-custom-fp" {
		t.Fatalf("client-supplied fingerprint must be honored verbatim, got %q", fp)
	}
}

func TestFingerprintStackVolatility(t *testing.T) {
	// Same frame, different line numbers/offsets → same fingerprint.
	a := Fingerprint("fin", "fatal", "nil pointer", "main.doThing (/app/main.go:42) +0x1a2b", "")
	b := Fingerprint("fin", "fatal", "nil pointer", "main.doThing (/app/main.go:99) +0x9f9f", "")
	if a != b {
		t.Fatalf("stack frames differing only by line/offset must group:\n  a=%s\n  b=%s", a, b)
	}
}

func TestNormalizeMessage(t *testing.T) {
	cases := map[string]string{
		"user 41 not found":                            "user <n> not found",
		"failed at 0xDEADBEEF":                         "failed at <addr>",
		"cannot open \"/etc/config.yaml\"":             "cannot open <str>",
		"request 550e8400-e29b-41d4-a716-446655440000": "request <uuid>",
	}
	for in, want := range cases {
		if got := normalizeMessage(in); got != want {
			t.Errorf("normalizeMessage(%q) = %q, want %q", in, got, want)
		}
	}
}
