package notify

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func ev(kind, siteID, name string, p payload, at time.Time) event {
	p.At = ts(at)
	return event{SiteID: siteID, SiteName: name, Kind: kind, Payload: p, CreatedAt: ts(at)}
}

func crashEvent(kind string, at time.Time) event {
	return ev(kind, "home", "Home", payload{
		GroupID: 42, Title: "TypeError: x is undefined", Level: "error", Environment: "prod",
		Release: "home@1.2.3", Message: "TypeError: x is undefined at render",
	}, at)
}

func TestSingleEventSubjects(t *testing.T) {
	code := 502
	cases := []struct {
		e    event
		want string
	}{
		{crashEvent(KindCrashNew, t0), "[status] Home: new crash: TypeError: x is undefined"},
		{crashEvent(KindCrashRegression, t0), "[status] Home: crash came back: TypeError: x is undefined"},
		{ev(KindFeedback, "home", "Home", payload{Ref: "R-7QK2", ReportKind: "bug", Message: "x"}, t0), "[status] Home: new bug report R-7QK2"},
		{ev(KindFeedback, "home", "Home", payload{Ref: "R-7QK2", ReportKind: "idea", Message: "x"}, t0), "[status] Home: new idea R-7QK2"},
		{ev(KindSiteDown, "home", "Home", payload{URL: "https://home.example.test", StatusCode: &code, DownSince: ts(t0)}, t0), "[status] Home is down"},
		{ev(KindSiteRecovered, "home", "Home", payload{URL: "https://home.example.test", DownSince: ts(t0)}, t0.Add(75*time.Minute)), "[status] Home is back up after 1h 15m"},
	}
	for _, c := range cases {
		if got := render([]event{c.e}, testPublicURL, t0).Subject; got != c.want {
			t.Errorf("%s: subject = %q, want %q", c.e.Kind, got, c.want)
		}
	}
}

func TestSummarySubjectCountsEveryKind(t *testing.T) {
	code := 500
	events := []event{
		crashEvent(KindCrashNew, t0),
		crashEvent(KindCrashNew, t0),
		ev(KindSiteDown, "fin", "Finance", payload{URL: "https://fin.example.test", StatusCode: &code, DownSince: ts(t0)}, t0),
		ev(KindFeedback, "home", "Home", payload{Ref: "R-AAAA", ReportKind: "bug"}, t0),
	}
	got := render(events, testPublicURL, t0).Subject
	if want := "[status] 4 updates: 1 site down, 2 new crashes, 1 report"; got != want {
		t.Fatalf("subject = %q, want %q", got, want)
	}
}

// TestADownAndItsRecoveryFoldIntoOneEntry: a blip inside one digest window is
// one line, not "down" and "back up" as two pieces of news.
func TestADownAndItsRecoveryFoldIntoOneEntry(t *testing.T) {
	code := 502
	events := []event{
		ev(KindSiteDown, "home", "Home", payload{URL: "https://home.example.test", StatusCode: &code, DownSince: ts(t0)}, t0),
		ev(KindSiteRecovered, "home", "Home", payload{URL: "https://home.example.test", DownSince: ts(t0)}, t0.Add(3*time.Minute)),
	}
	r := render(events, testPublicURL, t0.Add(4*time.Minute))
	if r.Subject != "[status] Home was down for 3m" {
		t.Fatalf("subject = %q", r.Subject)
	}
	if strings.Contains(r.Text, "is down") || strings.Contains(r.Text, "is back up") {
		t.Fatalf("the blip was listed twice:\n%s", r.Text)
	}
}

// TestSubjectsCannotInjectHeaders: a crash title is text somebody else chose. CR
// and LF in it would start a new header in any mailer that writes one.
func TestSubjectsCannotInjectHeaders(t *testing.T) {
	e := crashEvent(KindCrashNew, t0)
	e.Payload.Title = "boom\r\nBcc: attacker@example.test\x00"
	got := render([]event{e}, testPublicURL, t0).Subject
	if strings.ContainsAny(got, "\r\n\x00") {
		t.Fatalf("subject carries a control character: %q", got)
	}
	long := crashEvent(KindCrashNew, t0)
	long.Payload.Title = strings.Repeat("é", 500)
	if n := len([]rune(render([]event{long}, testPublicURL, t0).Subject)); n > maxSubjectRunes {
		t.Fatalf("subject is %d runes, want at most %d", n, maxSubjectRunes)
	}
}

// TestUserTextIsEscapedInHTML: a report's message goes into the HTML part as
// text, never as markup; the text part carries it verbatim.
func TestUserTextIsEscapedInHTML(t *testing.T) {
	e := ev(KindFeedback, "home", "<b>Home</b>", payload{Ref: "R-7QK2", ReportKind: "bug",
		Message: `<script>alert(1)</script><a href="javascript:x">click</a>`}, t0)
	r := render([]event{e}, testPublicURL, t0)
	for _, bad := range []string{"<script>", "<b>Home</b>", `href="javascript:`} {
		if strings.Contains(r.HTML, bad) {
			t.Fatalf("HTML carries %q unescaped:\n%s", bad, r.HTML)
		}
	}
	if !strings.Contains(r.HTML, "&lt;script&gt;") {
		t.Fatalf("HTML does not show the escaped text:\n%s", r.HTML)
	}
	if !strings.Contains(r.Text, "<script>alert(1)</script>") {
		t.Fatalf("the text part lost the message:\n%s", r.Text)
	}
}

func TestLongDigestsListTwentyFiveAndCountTheRest(t *testing.T) {
	var events []event
	for i := 0; i < 30; i++ {
		e := crashEvent(KindCrashNew, t0.Add(time.Duration(i)*time.Second))
		e.Payload.GroupID = int64(i + 1)
		events = append(events, e)
	}
	r := render(events, testPublicURL, t0)
	if n := strings.Count(r.Text, testPublicURL+"/crashes/"); n != 25 {
		t.Fatalf("text lists %d crashes, want 25", n)
	}
	if !strings.Contains(r.Text, "…and 5 more (5 new crashes)") {
		t.Fatalf("text does not count the rest:\n%s", r.Text)
	}
	if r.Subject != "[status] 30 updates: 30 new crashes" {
		t.Fatalf("subject = %q", r.Subject)
	}
}

func TestLinksAreBuiltOnThePublicURL(t *testing.T) {
	events := []event{
		crashEvent(KindCrashNew, t0),
		ev(KindFeedback, "home", "Home", payload{Ref: "R-7QK2", ReportKind: "bug"}, t0),
		ev(KindSiteDown, "my site", "Mine", payload{URL: "https://x.example.test", DownSince: ts(t0)}, t0),
	}
	r := render(events, "https://status.example.test", t0)
	for _, want := range []string{
		"https://status.example.test/crashes/42",
		"https://status.example.test/reports/R-7QK2",
		"https://status.example.test/sites/my%20site",
		"https://status.example.test/settings/notifications",
	} {
		if !strings.Contains(r.Text, want) || !strings.Contains(r.HTML, want) {
			t.Errorf("missing link %s", want)
		}
	}
}

// TestTheFooterDoesNotClaimASendTime: the email is rendered once and every
// retry sends it unchanged, possibly hours later — so the one time it states is
// when it was written, and it says so.
func TestTheFooterDoesNotClaimASendTime(t *testing.T) {
	r := render([]event{crashEvent(KindCrashNew, t0)}, "https://status.example.test", t0)
	want := "Written by status.example.test at 2026-09-25 14:00 UTC."
	for part, body := range map[string]string{"text": r.Text, "html": r.HTML} {
		if !strings.Contains(body, want) || strings.Contains(body, "Sent by") {
			t.Errorf("%s footer does not say %q:\n%s", part, want, body)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		20 * time.Second:               "under a minute",
		3 * time.Minute:                "3m",
		time.Hour:                      "1h",
		75 * time.Minute:               "1h 15m",
		26 * time.Hour:                 "1d 2h",
		48 * time.Hour:                 "2d",
		48*time.Hour + 20*time.Minute:  "2d",
		3*time.Minute + 40*time.Second: "4m",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestExcerptCollapsesAndCuts(t *testing.T) {
	if got := excerpt("a\n\n  b\tc", 100); got != "a b c" {
		t.Fatalf("excerpt = %q", got)
	}
	got := excerpt(strings.Repeat("ž", 400), 300)
	if n := len([]rune(got)); n != 300 || !strings.HasSuffix(got, "…") {
		t.Fatalf("excerpt is %d runes (%q…)", n, fmt.Sprint(got[:6]))
	}
}
