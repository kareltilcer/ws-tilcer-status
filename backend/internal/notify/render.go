package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// maxItems is how many entries one email lists before "…and N more". A bad
// deploy can open dozens of groups at once; the email is a pointer to the
// dashboard, not a copy of it.
const maxItems = 25

// maxSubjectRunes bounds the subject line. Mail clients cut far earlier.
const maxSubjectRunes = 140

// rendered is one email, ready to store on a digest.
type rendered struct {
	Subject string
	Text    string
	HTML    string
}

// item is one entry of an email: usually one event, or a down and its recovery
// folded together when both land in the same digest.
type item struct {
	Kind     string // an event kind, or kindBlip
	Headline string
	Details  []string
	Quote    string
	Link     string
	LinkText string
	at       string
	priority int
	subject  string // what the subject says when this is the only entry
}

// kindBlip is a site that went down and came back inside one digest window.
const kindBlip = "site_blip"

// render turns a digest's events into its subject and bodies. It is pure: the
// same events at the same `now` always give the same bytes, which matters
// because the result is stored and a retry must send it unchanged.
func render(events []event, publicURL string, now time.Time) rendered {
	items := buildItems(events, publicURL)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].priority != items[j].priority {
			return items[i].priority < items[j].priority
		}
		return items[i].at < items[j].at
	})

	var subject string
	if len(items) == 1 {
		subject = items[0].subject
	} else {
		subject = fmt.Sprintf("%d updates: %s", len(events), summarize(events))
	}
	subject = sanitizeSubject("[status] " + subject)

	shown, rest := items, []item(nil)
	if len(items) > maxItems {
		shown, rest = items[:maxItems], items[maxItems:]
	}
	more := ""
	if len(rest) > 0 {
		more = fmt.Sprintf("…and %d more (%s)", len(rest), summarizeItems(rest))
	}

	host := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		host = u.Host
	}
	intro := "1 update"
	if len(events) != 1 {
		intro = fmt.Sprintf("%d updates", len(events))
	}
	view := emailView{
		Host:        host,
		Intro:       intro,
		Items:       shown,
		More:        more,
		BoardURL:    publicURL + "/",
		SettingsURL: publicURL + "/settings/notifications",
		SentAt:      formatTime(now),
	}
	return rendered{Subject: subject, Text: renderText(view), HTML: renderHTML(view)}
}

// buildItems maps events to entries, folding a site_down and the site_recovered
// that follows it into one "was down for 3m" entry.
func buildItems(events []event, publicURL string) []item {
	var items []item
	openDown := map[string]int{} // site id → index of its unmatched down entry
	for _, e := range events {
		p := e.Payload
		site := e.SiteName
		if strings.TrimSpace(site) == "" {
			site = e.SiteID
		}
		siteLink := publicURL + "/sites/" + url.PathEscape(e.SiteID)
		switch e.Kind {
		case KindCrashNew, KindCrashRegression:
			head, subj, prio := "New crash on "+site, site+": new crash: "+p.Title, 2
			if e.Kind == KindCrashRegression {
				head, subj, prio = "A resolved crash came back on "+site, site+": crash came back: "+p.Title, 3
			}
			details := []string{crashMeta(p)}
			items = append(items, item{
				Kind: e.Kind, Headline: head, Details: details, Quote: p.Message,
				Link: publicURL + "/crashes/" + strconv.FormatInt(p.GroupID, 10), LinkText: "Open the crash group",
				at: e.CreatedAt, priority: prio, subject: subj,
			})
		case KindFeedback:
			noun := reportNoun(p.ReportKind)
			var details []string
			if p.Attachments > 0 {
				details = append(details, plural(p.Attachments, "attachment", "attachments")+" declared")
			}
			items = append(items, item{
				Kind: e.Kind, Headline: "New " + noun + " on " + site + " · " + p.Ref, Details: details,
				Quote: p.Message, Link: publicURL + "/reports/" + url.PathEscape(p.Ref), LinkText: "Open the report",
				at: e.CreatedAt, priority: 4, subject: site + ": new " + noun + " " + p.Ref,
			})
		case KindSiteDown:
			openDown[e.SiteID] = len(items)
			items = append(items, item{
				Kind: e.Kind, Headline: site + " is down", Details: []string{downDetail(p)},
				Link: siteLink, LinkText: "Open the site",
				at: e.CreatedAt, priority: 0, subject: site + " is down",
			})
		case KindSiteRecovered:
			if i, ok := openDown[e.SiteID]; ok {
				delete(openDown, e.SiteID)
				d := elapsed(p.DownSince, p.At)
				items[i] = item{
					Kind: kindBlip, Headline: site + " was down for " + d,
					Details: []string{fmt.Sprintf("%s — down at %s, back at %s", p.URL, formatStamp(p.DownSince), formatStamp(p.At))},
					Link:    siteLink, LinkText: "Open the site",
					at: items[i].at, priority: 1, subject: site + " was down for " + d,
				}
				continue
			}
			d := elapsed(p.DownSince, p.At)
			items = append(items, item{
				Kind: e.Kind, Headline: site + " is back up",
				Details: []string{fmt.Sprintf("%s — down since %s (%s)", p.URL, formatStamp(p.DownSince), d)},
				Link:    siteLink, LinkText: "Open the site",
				at: e.CreatedAt, priority: 1, subject: site + " is back up after " + d,
			})
		}
	}
	return items
}

func crashMeta(p payload) string {
	parts := []string{p.Level}
	if p.Environment != "" {
		parts = append(parts, p.Environment)
	} else {
		parts = append(parts, "no environment")
	}
	if p.Release != "" {
		parts = append(parts, p.Release)
	}
	parts = append(parts, formatStamp(p.At))
	return strings.Join(parts, " · ")
}

func downDetail(p payload) string {
	cause := "failing"
	switch {
	case p.StatusCode != nil:
		cause = "HTTP " + strconv.Itoa(*p.StatusCode)
	case p.Error != "":
		cause = p.Error
	}
	return fmt.Sprintf("%s — %s, since %s", p.URL, cause, formatStamp(p.DownSince))
}

func reportNoun(kind string) string {
	switch kind {
	case "bug":
		return "bug report"
	case "idea":
		return "idea"
	}
	return "report"
}

// summarize counts events by kind for a multi-event subject, in a fixed order.
func summarize(events []event) string {
	counts := map[string]int{}
	for _, e := range events {
		counts[e.Kind]++
	}
	return countPhrase(counts)
}

func summarizeItems(items []item) string {
	counts := map[string]int{}
	for _, it := range items {
		if it.Kind == kindBlip {
			counts[KindSiteDown]++
			counts[KindSiteRecovered]++
			continue
		}
		counts[it.Kind]++
	}
	return countPhrase(counts)
}

func countPhrase(counts map[string]int) string {
	order := []struct{ kind, one, many string }{
		{KindSiteDown, "site down", "sites down"},
		{KindSiteRecovered, "site back up", "sites back up"},
		{KindCrashNew, "new crash", "new crashes"},
		{KindCrashRegression, "crash came back", "crashes came back"},
		{KindFeedback, "report", "reports"},
	}
	var parts []string
	for _, o := range order {
		if n := counts[o.kind]; n > 0 {
			parts = append(parts, plural(n, o.one, o.many))
		}
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// sanitizeSubject makes a header-safe subject: every control character —
// CR and LF above all, which would otherwise start a new header — becomes a
// space, runs of whitespace collapse, and the result is cut to a length a mail
// client shows.
func sanitizeSubject(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return truncateRunes(strings.Join(strings.Fields(s), " "), maxSubjectRunes)
}

// excerpt collapses whitespace (a stack-like multi-line message reads as one
// paragraph in an email) and cuts to n runes.
func excerpt(s string, n int) string {
	return truncateRunes(strings.Join(strings.Fields(s), " "), n)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRightFunc(string(r[:n-1]), unicode.IsSpace) + "…"
}

// formatTime renders a time the way the email shows it: minutes, UTC, labelled.
func formatTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func formatStamp(s string) string {
	t, err := timeutil.Parse(s)
	if err != nil {
		return s
	}
	return formatTime(t)
}

// elapsed renders the time between two stored stamps as "3m", "1h 12m", "2d 3h".
func elapsed(from, to string) string {
	a, err1 := timeutil.Parse(from)
	b, err2 := timeutil.Parse(to)
	if err1 != nil || err2 != nil || b.Before(a) {
		return "a while"
	}
	return humanDuration(b.Sub(a))
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "under a minute"
	}
	days := int(d / (24 * time.Hour))
	hours := int(d % (24 * time.Hour) / time.Hour)
	mins := int(d % time.Hour / time.Minute)
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", mins)
}

// --- bodies -----------------------------------------------------------------

type emailView struct {
	Host        string
	Intro       string
	Items       []item
	More        string
	BoardURL    string
	SettingsURL string
	SentAt      string
}

func renderText(v emailView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", v.Host, v.Intro)
	for _, it := range v.Items {
		b.WriteString("\n* " + it.Headline + "\n")
		for _, d := range it.Details {
			b.WriteString("  " + d + "\n")
		}
		if it.Quote != "" {
			b.WriteString("  > " + it.Quote + "\n")
		}
		b.WriteString("  " + it.Link + "\n")
	}
	if v.More != "" {
		b.WriteString("\n" + v.More + " — " + v.BoardURL + "\n")
	}
	fmt.Fprintf(&b, "\n-- \nSent by %s at %s.\nChange what you get: %s\n", v.Host, v.SentAt, v.SettingsURL)
	return b.String()
}

// htmlTemplate is escaped contextually by html/template: every crash message and
// report excerpt in it is text somebody else wrote, and none of it is trusted.
var htmlTemplate = template.Must(template.New("digest").Parse(`<!doctype html>
<html><body style="margin:0;padding:24px;background:#f6f7f9;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1d2330;">
<div style="max-width:620px;margin:0 auto;background:#ffffff;border:1px solid #e3e6eb;border-radius:12px;padding:24px;">
<p style="margin:0 0 16px;font-size:13px;color:#5b6474;">{{.Host}} — {{.Intro}}</p>
{{range .Items}}<div style="border-top:1px solid #eceef2;padding:14px 0;">
<p style="margin:0 0 6px;font-size:15px;font-weight:700;">{{.Headline}}</p>
{{range .Details}}<p style="margin:0 0 4px;font-size:13px;color:#5b6474;">{{.}}</p>
{{end}}{{if .Quote}}<p style="margin:6px 0;padding:8px 10px;background:#f6f7f9;border-radius:6px;font-size:13px;white-space:pre-wrap;word-break:break-word;">{{.Quote}}</p>
{{end}}<p style="margin:6px 0 0;font-size:13px;"><a href="{{.Link}}" style="color:#2f5fd0;">{{.LinkText}}</a></p>
</div>
{{end}}{{if .More}}<p style="border-top:1px solid #eceef2;margin:0;padding-top:14px;font-size:13px;">{{.More}} — <a href="{{.BoardURL}}" style="color:#2f5fd0;">open the board</a></p>
{{end}}<p style="margin:20px 0 0;font-size:12px;color:#8a92a1;">Sent by {{.Host}} at {{.SentAt}}. <a href="{{.SettingsURL}}" style="color:#8a92a1;">Change what you get</a>.</p>
</div>
</body></html>
`))

func renderHTML(v emailView) string {
	var b bytes.Buffer
	if err := htmlTemplate.Execute(&b, v); err != nil {
		// The template is fixed and its data is strings: this cannot fail on
		// input, only on a broken template, which the render tests would catch.
		// The text part is always sent, so an empty HTML part degrades cleanly.
		return ""
	}
	return b.String()
}
