# Design Handoff — Status (Monitoring & Crash Reporting) Dashboard

> For: **Claude Design** · Owner: Karel · Last updated: 2026-08-01
> Source of truth: `PRD.md` (behaviour + data) and `openapi.yaml` (exact response shapes). This brief tells you **what to design and why**; those tell you **what data exists**.

## 1. Context

`status.tilcer.cz` is an internal dashboard that shows, at a glance, whether every service on Karel's droplet is healthy. Each monitored site has one of four states — **green / orange / red / unknown** — combining active uptime checks and incoming crash reports. It is one SPA (React + TypeScript + Vite + TanStack Query, static Nginx) behind login, part of the same fleet as `home.tilcer.cz` and `fin.tilcer.cz`.

Two functional areas surface in the UI: **monitoring** (is it up?) and **crash reporting** (is it throwing errors?).

## 2. Audience & usage context

- **Single user:** Karel (admin). No multi-user, no roles to design for.
- **Two modes of use:** (a) an occasional calm glance — "is everything green?"; (b) an incident — "something's red/orange, what and since when?". Design must serve both: reassuring when healthy, fast to triage when not.
- **Devices:** desktop-first (primary), but must be usable on a phone (glance from anywhere). Assume it's often left open on a second monitor.

## 3. Design goals

- **Glanceability first.** Overall fleet health should read in under ~2 seconds without interaction. The board is the product.
- **Calm when green, loud when red.** Healthy state should be quiet and low-contrast; problems should draw the eye. Avoid a wall of alarming color when all is well.
- **Never rely on color alone.** Every state must also carry a shape/icon and a text label (colorblind-safe, and required for accessibility — see §9).
- **Minimal chrome.** This is a personal ops tool, not a marketing surface. Density and clarity over decoration.
- **One identity per site.** The site `id` (e.g. `home`, `fin`) is the through-line — it appears in monitoring, in crashes, and in the integration snippet. Keep it visually consistent everywhere.

## 4. The status system (design the core of this carefully)

Four states, precedence **red > orange > green > unknown**:

| State | Meaning | When |
|---|---|---|
| 🟢 **green** | Healthy | Reachable and no crashes in the site's recent window (default 24h) |
| 🟠 **orange** | Noisy | Reachable but ≥1 recent crash in an open group |
| 🔴 **red** | Down | Failed health checks (debounced: only after 2 consecutive failures) |
| ⚪ **unknown** | No data yet | Monitored but not yet checked, and no crashes |

Design deliverables for this system: a **color token set** (with an accessible non-color cue per state), a compact **status pill/badge**, and a **legend**. Note the debounce nuance — a site can be failing one check but not yet red; consider whether to show a subtle "checking / degrading" affordance or simply hold the prior state (your call — propose it).

## 5. Screens & flows

Design each screen in its **default, empty, loading, and error** states. Data field names come from `openapi.yaml` (`SiteSummary`, `CrashGroup`, `CrashEvent`, `CheckResult`).

### 5.1 Board (`/`) — the home screen
- **Purpose:** whole-fleet health at a glance.
- **Content per site card:** name, `id`, status pill, last-checked time, open-crash-group count, recent-crash count. From `GET /api/sites` → `SiteSummary[]`.
- **Actions:** filter by state; click a card → site detail; "Add site".
- **States:** empty ("No sites yet — add your first"), loading (skeleton cards), error (inline retry; 401 → login).
- Auto-refreshes every ~30–60s; design a non-jarring refresh (no full-page flash).

### 5.2 Site detail (`/sites/:id`)
- **Purpose:** everything about one site.
- **Sections:**
  - **Uptime:** recent check history (`GET /api/sites/{id}/checks` → `CheckResult[]`) — an uptime strip/sparkline of ok/fail over time, latency shown but secondary.
  - **Crashes:** list of crash groups (`GET /api/sites/{id}/crashes` → `CrashGroup[]`) — title, level badge, count, first/last seen, status; filter by status/level.
  - **Config:** editable name, `monitor_url`, `monitor_enabled`, `expected_status`, `crash_window_hours`.
  - **Integration panel:** the ingest URL + a **"rotate key"** action, plus copyable Go/JS/`curl` snippets prefilled with this site's `id`.
- **States:** empty crash list ("No crashes 🎉"), empty check history, loading, error.

### 5.3 Add / edit site
- **Purpose:** register a site with a **user-supplied `id`**.
- **Form:** `id` (validated slug `^[a-z0-9][a-z0-9-]{0,62}$`, immutable after create), name, monitor_url (optional — omit for crash-only), options.
- **Critical moment — ingest key shown once:** on create (and on rotate), the API returns the plaintext ingest key **exactly once**. Design a modal/callout that: makes the key prominent, has a one-click **Copy**, clearly warns "you won't see this again — store it now / you can rotate later", and shows the ready-to-paste snippet. This is the highest-stakes microcopy in the app.

### 5.4 Crash group detail (`/crashes/:groupId`)
- **Purpose:** drill into one group of similar crashes.
- **Content:** group header (title, level, count, first/last seen, status) + a scrollable **event stream** (`GET /api/crashes/{groupId}`): message, stack trace (monospace, collapsible), environment, release, context (key/value). Paginated.
- **Actions:** resolve / ignore / reopen (`PATCH /api/crashes/{groupId}`). Resolving removes the group from the site's orange signal — reflect that consequence in the UI copy.
- **States:** loading, error, and a resolved/ignored visual treatment.

### 5.5 Login (Mode B)
- Self-hosted login page (status issues its own session, like `home`/`fin`). Design a minimal branded login; on 401 anywhere, route here.

## 6. Component inventory

Status pill/badge (4 states, with icon + label); site card; uptime strip/sparkline; crash-group row; **level badge** (fatal / error / warning — three distinct treatments); **key-reveal modal** (show-once, copy, warning); **copyable code snippet** block (tabbed Go / JS / curl); empty-state blocks; toast/confirmation for mutations (create, delete, rotate, resolve); filter controls.

## 7. Interaction & copy notes

- The **show-once key** flow is the make-or-break interaction — over-communicate irreversibility and offer rotate as the recovery path.
- **Destructive actions** (delete site, rotate key) need confirmation; deleting a site cascades its history — say so.
- Consider using the **`design:ux-copy`** skill for microcopy (empty states, the key warning, error messages, resolve/ignore confirmations).

## 8. Responsive & visual direction

- **Desktop-first**, graceful down to mobile: board becomes a single-column stack of cards; site detail sections stack.
- **Visual language:** align with the existing `home`/`fin` look and reuse their design tokens/system if one exists — this should feel like part of the same fleet, not a separate product. If no shared system exists, propose lightweight tokens (color, spacing, type) that could be reused.

## 9. Accessibility (required)

Target **WCAG 2.1 AA**. Specifically: status must never be conveyed by color alone (icon + label always); AA contrast for all text and the status colors chosen; keyboard-navigable board, forms, and modals; visible focus; adequate touch targets on mobile. **Run the `design:accessibility-review` skill on the designs before handoff.**

## 10. Expected deliverables from Claude Design

1. Hi-fi mockups of all five screens, each in default / empty / loading / error states.
2. The status color system: tokens, the accessible non-color cue per state, and the legend component.
3. Component specs for the inventory in §6 (variants + states), including the level badges and the show-once key modal.
4. A pass with **`design:accessibility-review`**, and then **`design:design-handoff`** to produce the engineering redline spec (tokens, spacing, component props, breakpoints) that complements `HANDOFF-engineering.md`.

## 11. Open design questions (for you to resolve/propose)

1. Board layout — card grid vs. dense table? Which reads faster at a glance for ~5–15 sites?
2. Debounce affordance — show a transient "1 failed check / checking" state, or just hold the prior color until red?
3. Uptime history — sparkline, colored strip (à la status-page bars), or both?
4. Light/dark — one theme or both? (Match `home`/`fin`.)

## 12. Review round 1 — required revisions

Your v1 (`Status.dc.html`) is a strong, faithful build — all five screens + legend, every state, correct color system with glyph+label cues, the 2-fail debounce affordance, the show-once key modal, `/readyz` targeting, and the fleet look reused from `home`. The following changes are needed before we lock the design. Decisions confirmed with Karel are noted so you don't need to re-ask.

**Confirmed decisions (apply, don't re-litigate):**
- **UI language: English only.** Keep it. (The Czech `home`/`fin` UIs are not the model here.)
- **Dark default + light** as you built it — good, keep both.
- **Debounce affordance** ("1 failed check · re-checking", pulsing dot) — good, keep it.

**Required changes:**

1. **Uptime is now a real API, not client-guessed.** The board `uptime_pct` and the site-detail strip/percentiles are backed by new contract (OpenAPI 0.2.0): `SiteSummary.uptime_pct` and `GET /api/sites/{id}/uptime` returning `{ uptime_pct, latency_p50_ms, latency_p95_ms, buckets[] }`, where each bucket is `{ start, end, ok_pct, checks, failed, latency_p50_ms }`. Design the strip against **`buckets[]`** (default 90 buckets), and handle a **gap bucket** (`ok_pct: null`, no checks) as a visually distinct state — not the same as 0% and not the same as a healthy bar. Show the p50/p95 figures from the endpoint, not invented.

2. **"Monitoring off" affordance (you flagged this well — now required).** A crash-only / monitoring-disabled site has `uptime_pct: null` and `monitor_enabled: false`. On the board card and site header, render a neutral **"monitoring off"** treatment in place of the uptime percentage. Such a site is never red — its pill reflects crashes only (green/orange). Avoid implying a green light means "actively confirmed up" when nothing is being polled.

3. **Confirmation + toast patterns (missing).** Design: (a) a **confirm dialog for destructive actions** — Delete site (warn it cascades all checks, rollups, crash groups & events) and Rotate key (warn the old key stops working immediately); (b) a **rotate-key result** reusing the show-once key modal; (c) a lightweight **toast/inline-confirm** for non-destructive mutations (save config, resolve/ignore a group).

4. **Responsive / mobile (missing).** You built desktop only. Add the small-screen treatment: how the 230px side nav collapses (drawer / top bar), the board as a single-column stack, and the site-detail two-column config/integration blocks stacking. Keep it usable one-handed for an incident glance.

**Deliverables still owed from §10:**

5. **Run `design:accessibility-review`** on the screens and return the report. Specifically verify AA contrast for the oklch status colors — the warn/orange text-on-soft-bg pill and the colored uptime figure are the likeliest failures. Confirm focus-visible on the board cards, form fields, and modals, and that the status is never conveyed by color alone (you've done the glyph+label — verify it holds in the pill's smallest size).

6. **Run `design:design-handoff`** to produce the engineering redline (tokens, spacing scale, component props, breakpoints) that complements `HANDOFF-engineering.md` for the frontend build (M5).

Everything else in v1 can stay as-is.
