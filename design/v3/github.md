repo: kareltilcer/ws-tilcer-status
branch: main
path: handoff/v3

## Last sync
date: 2026-09-02T19:45:00Z

### Updated in this project
- Designed the whole of **v3 (feedback)** from `handoff/v3` — design brief, engineering handoff and OpenAPI **0.3.0**.
- New `Feedback widget.dc.html`: the widget as a separate artifact with its own sRGB/system-font token set (V3-D55, explicitly not derived from status's oklch tokens) — launcher, dialog, uploading, success and all six §4.5 failure states, at 375 px and desktop, over a light and a dark host page, in Czech and English, plus token, component, copy-deck and accessibility deliverables.
- Answers to the five open questions (§11): icon-only launcher expanding to a labelled pill on hover/focus · disclosure as an always-visible summary that expands in place with a console opt-out · badge in the card's meta row in accent, never beside the status pill · bottom sheet under 480 px, centred dialog above · message before kind.
- `Status.dc.html` extended: cross-site **Inbox** (`/reports`) and **Report detail** (`/reports/:ref`) each in default / empty-or-text-only / loading / error, the board **unread badge** (`SiteSummary.open_reports`, null and 0 both render nothing), and the site-detail **feedback panel** with the kill switch, `console_capture`, widget-key rotate (reusing v2's show-once key modal) and the 503 "no object storage" treatment.
- Report states use a non-health palette (accent = "someone wrote to you", neutral = closed) so a report can never read as a degraded site; the page URL is text plus an explicit Open action.
- Accessibility and Redline screens gained v3 sections: measured contrast for the new chips/badge/console block, four new conformance notes, and seven new component contracts.

## Screen map
| Project screen | Built from (source) |
|---|---|
| Feedback widget.dc.html — all widget states, tokens, components, copy, a11y | handoff/v3/HANDOFF-design-v3.md §2–6, §8–11; openapi.yaml 0.3.0 (WidgetConfig, FeedbackSubmission, FeedbackAccepted, AttachmentState) |
| Status.dc.html — Inbox, Report detail | handoff/v3 §7.1–7.2; openapi.yaml `/api/reports`, `/api/reports/{ref}`, ReportSummary, Report, ReportPatch, AttachmentSummary |
| Status.dc.html — board unread badge | handoff/v3 §7.3; `SiteSummary.open_reports` (null when the module is not composed) |
| Status.dc.html — site-detail feedback panel | handoff/v3 §7.4; `/api/sites/{id}/feedback-config` (incl. 503), `/api/sites/{id}/rotate-widget-key` |
| Status.dc.html — v2 screens, tokens, shell | handoff/v2 HANDOFF-design.md §12; ws-tilcer-home frontend design system (dark-default oklch, Hanken Grotesk + IBM Plex Mono) |

## Sync history
- 2026-08-01T15:35:00Z — v2 handoff: uptime buckets from `/uptime`, monitoring-off affordance, confirm/toast patterns, responsive treatment, plus the owed Accessibility and Redline deliverables.
- 2026-08-01T10:30:00Z — v1 handoff: full hi-fi design (5 screens + legend, all states, status colour system, show-once key modal) from the spec-only handoff/v1 folder.

## Notes
- The repo carries the specs and the Go/React service; there is no built feedback UI to recreate — the design is produced from the handoff, the PRD §V3 sections and the contract.
- Two audiences, two visual languages, on purpose: the dashboard follows status (English, dark-default oklch, inline styles, no Tailwind); the widget shares no token, stylesheet or framework with it and renders into a closed shadow root.
- Still owed by engineering per §6.4: the three CSP directives in `docs/widget.md`, including `connect-src` for the R2 endpoint the PUT goes to directly.
