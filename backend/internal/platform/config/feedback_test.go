package config

import (
	"strings"
	"testing"
	"time"
)

// baseEnv is the minimum a development boot needs.
func baseEnv() map[string]string {
	return map[string]string{
		"STATUS_DB_PATH":         "/tmp/status.db",
		"STATUS_DEV_AUTH_BYPASS": "true",
	}
}

// TestFeedbackDefaultsAreOff: absence is the default state. A deployment that
// says nothing about feedback gets it off, with no R2 variable required.
func TestFeedbackDefaultsAreOff(t *testing.T) {
	c, err := Load(envGetter(baseEnv()))
	if err != nil {
		t.Fatalf("a configuration that never mentions feedback must load: %v", err)
	}
	if c.FeedbackEnabled {
		t.Fatal("feedback must default to off")
	}
	if c.FeedbackMaxFiles != 3 || c.FeedbackMaxImageBytes != 10<<20 || c.FeedbackMaxVideoBytes != 50<<20 {
		t.Fatalf("caps defaulted wrongly: files=%d image=%d video=%d", c.FeedbackMaxFiles, c.FeedbackMaxImageBytes, c.FeedbackMaxVideoBytes)
	}
	if c.FeedbackUploadTTL != 10*time.Minute || c.FeedbackViewTTL != 5*time.Minute || c.FeedbackUnclaimedTTL != 24*time.Hour {
		t.Fatalf("TTLs defaulted wrongly: upload=%s view=%s unclaimed=%s", c.FeedbackUploadTTL, c.FeedbackViewTTL, c.FeedbackUnclaimedTTL)
	}
	if c.FeedbackMinDwell != 3*time.Second {
		t.Fatalf("min dwell = %s, want 3s", c.FeedbackMinDwell)
	}
}

// TestFeedbackEnabledRequiresEveryStorageVar: the switch must not be flippable
// into a state the process cannot serve, and the loader lists every problem at
// once rather than making the operator discover them one boot at a time.
func TestFeedbackEnabledRequiresEveryStorageVar(t *testing.T) {
	env := baseEnv()
	env["STATUS_FEEDBACK_ENABLED"] = "true"

	_, err := Load(envGetter(env))
	if err == nil {
		t.Fatal("expected boot to fail when feedback is enabled with no object storage configured")
	}
	for _, want := range []string{
		"STATUS_R2_ENDPOINT",
		"STATUS_R2_BUCKET",
		"STATUS_R2_ACCESS_KEY_ID",
		"STATUS_R2_SECRET_ACCESS_KEY",
		"STATUS_FEEDBACK_TICKET_SECRET",
		"STATUS_IP_HASH_SALT",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error should name every missing variable at once; %s is absent from: %v", want, err)
		}
	}

	// With all of them present it loads.
	env["STATUS_R2_ENDPOINT"] = "https://account.r2.cloudflarestorage.com"
	env["STATUS_R2_BUCKET"] = "ws-tilcer-status-feedback"
	env["STATUS_R2_ACCESS_KEY_ID"] = "id"
	env["STATUS_R2_SECRET_ACCESS_KEY"] = "SHOULD-NEVER-BE-LOGGED-r2"
	env["STATUS_FEEDBACK_TICKET_SECRET"] = "SHOULD-NEVER-BE-LOGGED-ticket"
	env["STATUS_IP_HASH_SALT"] = "SHOULD-NEVER-BE-LOGGED-salt"
	c, err := Load(envGetter(env))
	if err != nil {
		t.Fatalf("a fully configured feedback deployment must load: %v", err)
	}
	if !c.FeedbackEnabled || c.R2Bucket != "ws-tilcer-status-feedback" {
		t.Fatalf("configuration did not take: %+v", c.R2Bucket)
	}
	// ⚠ The secrets must never reach a log line.
	if red := c.Redacted(); strings.Contains(red, "SHOULD-NEVER-BE-LOGGED") {
		t.Fatalf("Redacted() leaked a secret into the boot log line: %s", red)
	}
}

// TestUploadTTLMustBeShorterThanUnclaimedTTL is V3-D06's "a GC that outruns an
// in-flight upload" arriving through the config file rather than through the code.
func TestUploadTTLMustBeShorterThanUnclaimedTTL(t *testing.T) {
	env := baseEnv()
	env["STATUS_FEEDBACK_UPLOAD_TTL"] = "48h"
	env["STATUS_FEEDBACK_UNCLAIMED_TTL"] = "24h"

	_, err := Load(envGetter(env))
	if err == nil {
		t.Fatal("expected an error when the upload TTL outlives the sweep threshold")
	}
	if !strings.Contains(err.Error(), "STATUS_FEEDBACK_UPLOAD_TTL") || !strings.Contains(err.Error(), "STATUS_FEEDBACK_UNCLAIMED_TTL") {
		t.Fatalf("the error should name both vars, got: %v", err)
	}

	env["STATUS_FEEDBACK_UPLOAD_TTL"] = "10m"
	if _, err := Load(envGetter(env)); err != nil {
		t.Fatalf("an upload TTL well inside the sweep threshold must be valid: %v", err)
	}
}

// TestVideoCapMustNotBeBelowImageCap — a video cap below the image cap is always
// a typo.
func TestVideoCapMustNotBeBelowImageCap(t *testing.T) {
	env := baseEnv()
	env["STATUS_FEEDBACK_MAX_IMAGE_MB"] = "10"
	env["STATUS_FEEDBACK_MAX_VIDEO_MB"] = "5"

	_, err := Load(envGetter(env))
	if err == nil {
		t.Fatal("expected an error when the video cap is below the image cap")
	}
	if !strings.Contains(err.Error(), "STATUS_FEEDBACK_MAX_VIDEO_MB") {
		t.Fatalf("the error should name the video cap, got: %v", err)
	}
}

// TestMinDwellCeiling — a dwell longer than half a minute is a disabled widget
// wearing a config value.
func TestMinDwellCeiling(t *testing.T) {
	env := baseEnv()
	env["STATUS_FEEDBACK_MIN_DWELL_MS"] = "45000"

	_, err := Load(envGetter(env))
	if err == nil {
		t.Fatal("expected an error for a 45-second minimum dwell")
	}
	if !strings.Contains(err.Error(), "STATUS_FEEDBACK_MIN_DWELL_MS") {
		t.Fatalf("the error should name the dwell var, got: %v", err)
	}
}

// TestFeedbackRateUsesTheRateIdiom: STATUS_FEEDBACK_RATE reads the same "N/unit"
// forms as STATUS_INGEST_RATE, so an operator does not have to know that one is a
// per-second float.
func TestFeedbackRateUsesTheRateIdiom(t *testing.T) {
	env := baseEnv()
	env["STATUS_FEEDBACK_RATE"] = "20/h"
	env["STATUS_FEEDBACK_IP_RATE"] = "5/h"

	c, err := Load(envGetter(env))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.FeedbackRatePerSec * 3600; got < 19.9 || got > 20.1 {
		t.Fatalf("20/h parsed as %.4f/s (%.2f/hour)", c.FeedbackRatePerSec, got)
	}
	if got := c.FeedbackIPRatePerSec * 3600; got < 4.9 || got > 5.1 {
		t.Fatalf("5/h parsed as %.4f/s (%.2f/hour)", c.FeedbackIPRatePerSec, got)
	}
}
