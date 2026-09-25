package config

import (
	"strings"
	"testing"
	"time"
)

// TestNotificationDefaults: a deployment that says nothing about email loads,
// with no provider and the documented defaults.
func TestNotificationDefaults(t *testing.T) {
	c, err := Load(envGetter(baseEnv()))
	if err != nil {
		t.Fatalf("a configuration that never mentions email must load: %v", err)
	}
	if c.ResendAPIKey != "" || c.MailFrom != "tilcer status <status@tilcer.cz>" ||
		c.PublicURL != "https://status.tilcer.cz" || c.NotifyDigestWindow != 2*time.Minute || c.NotifyMaxPerHour != 6 {
		t.Fatalf("defaults: key=%q from=%q url=%q window=%s cap=%d",
			c.ResendAPIKey, c.MailFrom, c.PublicURL, c.NotifyDigestWindow, c.NotifyMaxPerHour)
	}
}

// TestProductionWithoutAKeyStillBoots: email is optional. A production
// deployment without a key runs without notifications; it does not refuse to
// start the way a missing auth secret does.
func TestProductionWithoutAKeyStillBoots(t *testing.T) {
	env := map[string]string{
		"STATUS_ENV":                 "production",
		"STATUS_DB_PATH":             "/data/status.db",
		"AUTH_BASE_URL":              "https://auth.tilcer.cz",
		"STATUS_AUTH_SERVICE_SECRET": "s",
		"STATUS_AUTH_JWT_SECRET":     "j",
	}
	if _, err := Load(envGetter(env)); err != nil {
		t.Fatalf("production without a Resend key must boot: %v", err)
	}
}

func TestPublicURLIsTrimmedAndValidated(t *testing.T) {
	env := baseEnv()
	env["STATUS_PUBLIC_URL"] = " https://status.example.test/ "
	c, err := Load(envGetter(env))
	if err != nil || c.PublicURL != "https://status.example.test" {
		t.Fatalf("url = %q (%v), want the trailing slash trimmed", c.PublicURL, err)
	}
	for _, bad := range []string{
		"status.tilcer.cz",                 // relative
		"ftp://status.tilcer.cz",           // not http(s)
		"https://status.tilcer.cz/?a=1",    // a query would land in every link
		"https://status.tilcer.cz/#board",  // so would a fragment
		"https://user:pw@status.tilcer.cz", // and credentials
		"https://",                         // no host
	} {
		env["STATUS_PUBLIC_URL"] = bad
		if _, err := Load(envGetter(env)); err == nil || !strings.Contains(err.Error(), "STATUS_PUBLIC_URL") {
			t.Errorf("%q was accepted (%v)", bad, err)
		}
	}
}

func TestNotificationRanges(t *testing.T) {
	cases := map[string]string{
		"STATUS_MAIL_FROM":            "not an address",
		"STATUS_NOTIFY_DIGEST_WINDOW": "2h",
		"STATUS_NOTIFY_MAX_PER_HOUR":  "0",
	}
	for key, bad := range cases {
		env := baseEnv()
		env[key] = bad
		if _, err := Load(envGetter(env)); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%q was accepted (%v)", key, bad, err)
		}
	}
	env := baseEnv()
	env["STATUS_NOTIFY_MAX_PER_HOUR"] = "61"
	if _, err := Load(envGetter(env)); err == nil {
		t.Error("a cap above 60 an hour was accepted")
	}
	env = baseEnv()
	env["STATUS_NOTIFY_DIGEST_WINDOW"] = "0s"
	if _, err := Load(envGetter(env)); err != nil {
		t.Errorf("a zero window (send as soon as the worker runs) must be allowed: %v", err)
	}
}

func TestTheResendKeyIsNeverLogged(t *testing.T) {
	env := baseEnv()
	env["STATUS_RESEND_API_KEY"] = "re_SHOULD-NEVER-BE-LOGGED"
	c, err := Load(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	if red := c.Redacted(); strings.Contains(red, "SHOULD-NEVER-BE-LOGGED") || !strings.Contains(red, "resend_key=set(***)") {
		t.Fatalf("redacted config leaks or hides the key's presence: %s", red)
	}
}
