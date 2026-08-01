# PRD — Status (Monitoring & Crash Reporting)

> Status: Draft · Owner: Karel · Last updated: 2026-08-01
> Companion spec: `openapi.yaml` (OpenAPI 3.1)

## 1. Overview

- **One-line summary:** A single service that actively monitors the uptime of registered sites and ingests crash reports from them, surfacing a simple green/orange/red status per site on a dashboard.
- **Type:** fe/be pair (two Coolify apps, one origin — mirrors `home`/`fin`).
- **Subdomain:** `status.tilcer.cz`
- **Exposure:** public. The dashboard (SPA) is publicly routed but sits behind the shared auth backend; the crash-ingest API is public and authenticated per-site by an ingest key (Sentry-DSN style).
- **Consumers:**
  - Karel (single admin) via the dashboard.
  - Every other backend on the droplet (Go services) posting crash reports.
  - Frontends (browser JS) posting client-side errors.
  - The monitoring poller itself, which calls each monitored site's health endpoint.
- **Depends on:** `auth` (site id `status`) for dashboard/admin auth. No other service dependencies. Litestream → R2 for backup.

### Modules

The backend is a **compile-time modular monolith** (same pattern as `home`), with two functional modules plus shared infrastructure:

1. **`monitoring`** — background poller + check history + per-site reachability state.
2. **`crash`** — public ingest, fingerprint-based grouping, crash groups/events, group triage.
3. **`dashboard`** (frontend) — the green/orange/red board and drill-downs.

Shared: `sites` registry (the join point — a site id is defined once and used by both modules), `logging`, health probes.

### Deliverables (implementation)

Beyond the running service, the implementation **must** ship, as first-class deliverables (see FR-12):

- **`README.md` quick-start** — the shortest path to sending your first crash: create a site → copy the ingest key → paste a snippet → see it on the board, in under a few minutes.
- **Detailed integration docs** — full reference for the ingest payload, fingerprinting, environments/releases, error handling, and rate limits.
- **Copy-in client helpers** — a tiny Go helper (report an `error`/`panic` with `recover`) and a browser JS snippet (`window.onerror` / `unhandledrejection` hook). These are copy-in, not published/versioned packages.

## 2. Goals & Non-Goals

**Goals**

- One glance tells Karel whether every site is healthy (green), noisy (orange), or down (red).
- Add/remove monitored sites from the dashboard; each site is identified by a **user-supplied id** (e.g. `home`, `fin`, `yarnlog`).
- The **same site id** is used both for monitoring config and as the target of crash reports — one identity across both modules.
- Zero-friction crash reporting: a backend or frontend posts a JSON crash to a public endpoint with a per-site ingest key.
- Similar crashes are grouped so counts are meaningful rather than a flat firehose.
- Cheap to run: single droplet, embedded SQLite, 5-minute polling, 90-day rolling retention.

**Non-Goals (v1)**

- No push alerting (email/webhook/chat). Dashboard-only. (Design leaves room to add it later.)
- No latency/degraded-performance signal driving status color (reachable is binary in v1).
- No multi-user / multi-tenant / team roles — single admin.
- No distributed/HA polling; one poller instance.
- No breadcrumbs, release-regression tracking, source maps, or symbolication.
- No public status page for end users — the board is entirely behind auth in v1 (revisit later).
- No synthetic transaction/multi-step checks — a single HTTP GET per site.
- No published/versioned client SDK packages — v1 ships copy-in Go + JS helper snippets and docs only.

## 3. Users & Roles

- **Admin (Karel)** — the only interactive user. Full access: manage sites, view checks, view/triage crashes, rotate ingest keys. Authenticated via the shared `auth` backend as site `status` (session cookie for long-term + JWT 15-min for API calls), following the `home`/`fin` pattern (self-hosted login + own session, "Mode B").
- **Reporting clients (machine)** — backends and frontends. No user identity; authenticated only by a per-site **ingest key** scoped to the `crash` ingest endpoint. They cannot read anything, only POST crashes for their own site.
- **Monitoring poller (internal)** — not a user; a background goroutine that reaches out to monitored sites.

## 4. Functional Requirements

### FR-1: Register a monitored site
- **Description:** Create a site entry that both modules key off of.
- **Trigger:** Admin action in dashboard (`POST /api/sites`).
- **Inputs:**
  - `id` (string, **user-supplied**, required) — slug, `^[a-z0-9][a-z0-9-]{0,62}$`, unique (primary key).
  - `name` (string, required) — display label.
  - `monitor_url` (string URL, optional) — endpoint to poll; if absent, monitoring is disabled for this site (crash-only site).
  - `monitor_enabled` (bool, default true when `monitor_url` set).
  - `expected_status` (int, default 200) — HTTP status treated as healthy; any 2xx accepted if left default.
  - `crash_window_hours` (int, default 24) — window for "recent crashes" that drives orange.
- **Behaviour:** Validate id format + uniqueness. Generate a random **ingest key** (`ik_` + 32 url-safe bytes), store a hash of it, return the plaintext key **once**. Persist site. Initial cached status = `unknown` until first check/crash.
- **Outputs:** `201` with the created site + the plaintext `ingest_key` (shown once).
- **Errors:** `409` duplicate id; `422` invalid id/url; `401` unauthenticated.

### FR-2: List sites with current status
- **Description:** The dashboard board — every site with its color and headline numbers.
- **Trigger:** `GET /api/sites`.
- **Inputs:** none (optional `?status=green|orange|red|unknown` filter).
- **Behaviour:** For each site compute the color (see FR-6) from the latest check + recent crash count. Include open crash-group count and last-check time.
- **Outputs:** `200` array of `{ id, name, color, monitor_enabled, last_checked_at, last_ok, uptime_pct, open_crash_groups, recent_crash_count }`. `uptime_pct` is the rolling uptime over `UPTIME_WINDOW` (default 90d) from rollups, and is `null` for monitoring-disabled or not-yet-checked sites (the UI then shows a "monitoring off" affordance instead of a percentage).
- **Errors:** `401`.

### FR-3: Get / update / delete a site
- **Description:** Site detail and edits.
- **Trigger:** `GET|PATCH|DELETE /api/sites/{id}`.
- **Inputs:** PATCH accepts `name`, `monitor_url`, `monitor_enabled`, `expected_status`, `crash_window_hours`.
- **Behaviour:** PATCH updates config (id is immutable). DELETE removes the site and cascades its checks, crash events, and crash groups.
- **Outputs:** `200` site (with computed color + counts) / `204` on delete.
- **Errors:** `404` unknown id; `422` validation; `401`.

### FR-4: Rotate ingest key
- **Description:** Invalidate the old ingest key and issue a new one.
- **Trigger:** `POST /api/sites/{id}/rotate-key`.
- **Behaviour:** Generate + hash a new key, replace, return plaintext once. Old key stops working immediately.
- **Outputs:** `200 { ingest_key }`.
- **Errors:** `404`; `401`.

### FR-5: Poll monitored sites (background)
- **Description:** Actively check reachability of every monitor-enabled site.
- **Trigger:** Scheduled — every **5 minutes** (global default; configurable via env `CHECK_INTERVAL`).
- **Inputs:** each site's `monitor_url`, `expected_status`.
- **Behaviour:** For each enabled site, HTTP GET `monitor_url` with a timeout (`CHECK_TIMEOUT`, default 10s), following redirects. A check is `ok` if the response status matches `expected_status` (or is any 2xx when default). Record `check_result { site_id, checked_at, ok, status_code, latency_ms, error }`. Maintain `site.fail_streak` (increment on failure, reset to 0 on success) for the red debounce (FR-6). Update the site's cached reachability. Poller runs in one goroutine, sequential or bounded-concurrency; failures on one site never block others.
- **Recommended target:** for the droplet's own Go services, point `monitor_url` at **`/readyz`** (not `/healthz`) so that "green" means actually able to serve — `/readyz` includes the SQLite connectivity check. `monitor_url` remains an explicit full URL per site; this is documented guidance, not an enforced path.
- **Outputs:** rows in `check_result`; updated cached status.
- **Errors:** network/timeout/DNS failures are recorded as `ok=false` with the error string — they are data, not service errors.

### FR-6: Compute status color
- **Description:** Deterministic mapping to green/orange/red per site.
- **Trigger:** On read (FR-2/FR-3) and cached after each check/ingest.
- **Behaviour (v1 rules):**
  - **red** — monitoring enabled and `fail_streak >= RED_FAIL_THRESHOLD` (default **2** consecutive failed checks). A single failed check does **not** flip red — this 2-fail debounce avoids brief blips; at 5-min polling, red is declared ~10 min after a site goes down.
  - **orange** — reachable (not red) **and** there is ≥1 crash event within `crash_window_hours` in an open (non-resolved/ignored) group.
  - **green** — reachable (not red) and no qualifying crashes within the window.
  - **unknown** — monitor-enabled site with no check yet, and no crashes.
  - **Monitoring disabled (crash-only site):** never red (no checks to fail). Color is orange if it has qualifying crashes, else green. Its `uptime_pct` is `null` and the UI must render a neutral **"monitoring off"** affordance in place of an uptime figure — green here means "no problems reported," not "actively confirmed up."
  - Precedence: red > orange > green > unknown. A down site (streak ≥ threshold) with crashes is still red.
  - **Transient note:** a site with 1 failure (streak below threshold) keeps its prior green/orange color until the streak reaches the threshold.
- **Outputs:** one of `red|orange|green|unknown`.
- **Errors:** n/a (pure function).

### FR-7: Ingest a crash report (public)
- **Description:** Accept a crash/error event from a backend or frontend.
- **Trigger:** `POST /api/ingest/{siteId}` with header `X-Ingest-Key`.
- **Inputs (body):**
  - `message` (string, required).
  - `level` (enum `fatal|error|warning`, default `error`).
  - `stack` (string, optional).
  - `environment` (string, optional, e.g. `prod`/`dev`).
  - `release` (string, optional).
  - `fingerprint` (string, optional) — client-supplied grouping override.
  - `context` (object, optional, free-form; size-capped) — tags, request info, user hints, etc.
  - `occurred_at` (RFC3339, optional; server uses receipt time if absent).
- **Behaviour:** Validate `{siteId}` exists and `X-Ingest-Key` matches the stored hash. Enforce body size cap (`MAX_INGEST_BYTES`, default 64 KB) and per-site-key rate limit (`INGEST_RATE`, default e.g. 60/min, burst 120). Compute a **fingerprint**: use the client value if given, else hash of `site_id + level + normalized(message) + first stack frame`. Upsert the `crash_group` for that fingerprint (create with `first_seen`, else bump `last_seen` + `count`, reopen if it was resolved and `REOPEN_ON_REGRESSION` is set). Insert the `crash_event`. Update the site's cached color.
- **Outputs:** `202 { group_id, event_id }`.
- **Errors:** `401` bad/missing key; `404` unknown site; `413` payload too large; `422` invalid body; `429` rate-limited.

### FR-8: Browse crashes
- **Description:** View crash groups for a site and drill into a group's events.
- **Trigger:** `GET /api/sites/{id}/crashes`, `GET /api/crashes/{groupId}`.
- **Inputs:** filters `?status=open|resolved|ignored`, `?level=`, pagination `?limit&cursor`.
- **Behaviour:** List groups sorted by `last_seen` desc with `count`, `level`, `title`, `first_seen`, `last_seen`, `status`. Group detail returns the group plus a paginated list of recent events (message, stack, environment, release, context, occurred_at).
- **Outputs:** `200`.
- **Errors:** `404`; `401`.

### FR-9: Triage a crash group
- **Description:** Mark a group resolved/ignored/open.
- **Trigger:** `PATCH /api/crashes/{groupId}` `{ status }`.
- **Behaviour:** Update group status. `resolved`/`ignored` groups no longer count toward the site's orange signal. (A new event may reopen a resolved group per `REOPEN_ON_REGRESSION`.)
- **Outputs:** `200` group.
- **Errors:** `404`; `422`; `401`.

### FR-10: Retention purge (background)
- **Description:** Keep the DB lean.
- **Trigger:** Scheduled daily.
- **Behaviour:** Delete `check_result` and `crash_event` rows older than **90 days** (`RETENTION_DAYS`); delete `check_rollup` rows older than `ROLLUP_RETENTION_DAYS` (default 400). Crash groups with no events left in the window are deleted (or emptied). Runs as a single sweep **after** the daily rollup job (FR-13) so no check data is lost before it is aggregated; Litestream replicates the deletions.
- **Outputs:** none (logged).
- **Errors:** logged; non-fatal.

### FR-11: Health probes
- **Description:** Baseline observability.
- **Trigger:** `GET /healthz`, `GET /readyz`.
- **Behaviour:** `/healthz` returns process-up; `/readyz` additionally checks SQLite connectivity. Both are public, unauthenticated. (These are also natural `monitor_url` targets for other services.)

### FR-12: Integration documentation, quick-start & client helpers
- **Description:** First-class documentation and copy-in client code so wiring crash reporting into any project is trivial. This is a required deliverable, not optional polish.
- **Trigger:** Part of the build; verified at handoff.
- **Inputs:** the final ingest contract (FR-7).
- **Behaviour / contents:**
  - **`README.md` quick-start** — a numbered, few-minute path: (1) add a site in the dashboard, (2) copy the ingest URL + `X-Ingest-Key`, (3) drop in the Go helper or JS snippet (or a raw `curl`/`fetch`), (4) trigger a test error, (5) see it appear on the board. Includes one runnable `curl` example.
  - **Detailed integration docs** — full ingest reference: endpoint, headers, every payload field, `level` semantics, `environment`/`release` conventions, client-supplied `fingerprint`, `context` shape and size cap, rate-limit behaviour (`429`) and recommended client-side backoff/drop, and payload-size cap (`413`).
  - **Go helper** — a small copy-in package: a `Report(err, opts)` call and a `defer Recover()` panic handler that POST to `/api/ingest/{siteId}` with the key from an env var; fire-and-forget, never blocks or crashes the host app on ingest failure.
  - **Browser JS snippet** — a minimal client hooking `window.onerror` and `unhandledrejection`, posting via `fetch` with `keepalive`; documents that the browser ingest key is public by design (like a Sentry DSN).
  - **Copy-paste "wire up an existing service" checklist** — the exact steps to add reporting to another `ws-tilcer-*` service.
- **Outputs:** `README.md` + `docs/` in the repo; helper files under a clearly named path (e.g. `clients/go/`, `clients/js/`).
- **Errors:** n/a.

### FR-13: Aggregated uptime & latency
- **Description:** Serve rolling uptime % and latency percentiles per site — for the board card figure and the site-detail uptime strip — without scanning raw checks.
- **Trigger:** Nightly **rollup job** writes daily aggregates; `GET /api/sites/{id}/uptime` reads them; the board's `uptime_pct` is refreshed from them.
- **Behaviour:**
  - A daily job aggregates each site's `check_result` rows for the day into a `check_rollup` row: `ok_count`, `fail_count`, and `latency_p50_ms` / `latency_p95_ms` computed from that day's samples. It runs **before** the retention purge (FR-10) so no data is lost, and it also refreshes each site's cached `uptime_pct` over `UPTIME_WINDOW` (default 90d).
  - `GET /api/sites/{id}/uptime?window=&buckets=` returns overall `uptime_pct`, `checks_total`, `checks_failed`, `latency_p50_ms`, `latency_p95_ms`, and a `buckets[]` array (each `{ start, end, ok_pct, checks, failed, latency_p50_ms }`) sized to `buckets` for the strip. A bucket with no checks has `ok_pct: null` (a gap), distinct from `0`.
  - `uptime_pct` is `null` for monitoring-disabled or never-checked sites.
- **Outputs:** `UptimeSummary` (see `openapi.yaml`); cached `site.uptime_pct`.
- **Errors:** `404` unknown site; `401`.
- **Why a rollup:** at 5-min polling, 90 days ≈ 25.9k `check_result` rows per site — too many to scan per board load. Daily rollups make both the board field and the strip cheap, and (retained longer than raw checks) preserve long-window history past the 90-day raw purge.

## 5. Data Model

SQLite (embedded, `modernc.org/sqlite`), migrations via Goose.

**`site`**
| col | type | notes |
|---|---|---|
| `id` | TEXT PK | user-supplied slug |
| `name` | TEXT NOT NULL | |
| `monitor_url` | TEXT NULL | poll target; null = crash-only |
| `monitor_enabled` | INTEGER NOT NULL DEFAULT 1 | 0/1 |
| `expected_status` | INTEGER NOT NULL DEFAULT 200 | |
| `crash_window_hours` | INTEGER NOT NULL DEFAULT 24 | drives orange |
| `ingest_key_hash` | TEXT NOT NULL | hash of the ingest key; plaintext never stored |
| `cached_color` | TEXT NOT NULL DEFAULT 'unknown' | red/orange/green/unknown |
| `last_checked_at` | TEXT NULL | RFC3339 |
| `last_ok` | INTEGER NULL | last check result |
| `fail_streak` | INTEGER NOT NULL DEFAULT 0 | consecutive failed checks; drives red debounce |
| `uptime_pct` | REAL NULL | cached rolling uptime over `UPTIME_WINDOW`; null if disabled/unchecked; refreshed by rollup job |
| `created_at` | TEXT NOT NULL | |

**`check_result`** — index `(site_id, checked_at DESC)`
| col | type | notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | |
| `site_id` | TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE | |
| `checked_at` | TEXT NOT NULL | |
| `ok` | INTEGER NOT NULL | |
| `status_code` | INTEGER NULL | |
| `latency_ms` | INTEGER NULL | recorded but not used for color in v1 |
| `error` | TEXT NULL | |

**`check_rollup`** — PK `(site_id, day)`, index `(site_id, day DESC)`
| col | type | notes |
|---|---|---|
| `site_id` | TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE | |
| `day` | TEXT NOT NULL | UTC date `YYYY-MM-DD` |
| `ok_count` | INTEGER NOT NULL | successful checks that day |
| `fail_count` | INTEGER NOT NULL | failed checks that day |
| `latency_p50_ms` | INTEGER NULL | from that day's ok samples |
| `latency_p95_ms` | INTEGER NULL | from that day's ok samples |

Retained longer than raw checks (`ROLLUP_RETENTION_DAYS`, default 400) so long windows survive the 90-day raw-check purge.

**`crash_group`** — unique `(site_id, fingerprint)`, index `(site_id, last_seen DESC)`
| col | type | notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | |
| `site_id` | TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE | |
| `fingerprint` | TEXT NOT NULL | |
| `title` | TEXT NOT NULL | derived from first event message |
| `level` | TEXT NOT NULL | highest level seen |
| `count` | INTEGER NOT NULL DEFAULT 0 | |
| `status` | TEXT NOT NULL DEFAULT 'open' | open/resolved/ignored |
| `first_seen` | TEXT NOT NULL | |
| `last_seen` | TEXT NOT NULL | |

**`crash_event`** — index `(site_id, occurred_at DESC)`, `(group_id, occurred_at DESC)`
| col | type | notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | |
| `site_id` | TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE | |
| `group_id` | INTEGER NOT NULL REFERENCES crash_group(id) ON DELETE CASCADE | |
| `level` | TEXT NOT NULL | |
| `message` | TEXT NOT NULL | |
| `stack` | TEXT NULL | |
| `environment` | TEXT NULL | |
| `release` | TEXT NULL | |
| `context` | TEXT NULL | JSON blob |
| `occurred_at` | TEXT NOT NULL | client-supplied or receipt time |
| `received_at` | TEXT NOT NULL | server receipt |

**Goose notes:** initial migration creates all five tables (`site`, `check_result`, `check_rollup`, `crash_group`, `crash_event`) + indexes and enables `PRAGMA foreign_keys=ON`. Expected future changes: alert-config table (when push alerting lands), optional `latency_threshold_ms` on `site` (if degraded/orange-by-latency returns), `client` column on `crash_event` (fe/be provenance).

## 6. API Surface

Full detail in `openapi.yaml`. Backend served at `status.tilcer.cz/api` (no strip-prefix, per the two-app deploy pattern).

**Auth per group**
- `/healthz`, `/readyz` — public, no auth.
- `POST /api/ingest/{siteId}` — public, `X-Ingest-Key` header (per-site). Rate-limited, size-capped.
- Everything else under `/api/**` (sites, checks, crashes) — admin only, `bearerAuth` (JWT) with session cookie fallback, via `auth` site `status`.

**Endpoints**
- `GET /api/sites`, `POST /api/sites`
- `GET|PATCH|DELETE /api/sites/{id}`
- `POST /api/sites/{id}/rotate-key`
- `GET /api/sites/{id}/checks`
- `GET /api/sites/{id}/uptime` — aggregated uptime/latency for the board figure + detail strip (rollup-backed)
- `GET /api/sites/{id}/crashes`
- `GET /api/crashes/{groupId}`, `PATCH /api/crashes/{groupId}`
- `POST /api/ingest/{siteId}` (public)

**Conventions:** cursor pagination (`limit` + opaque `cursor`) on `checks` and `crashes` lists; filters via query params; RFC3339 timestamps; JSON everywhere.

## 7. Frontend (Dashboard)

React + TypeScript + Vite SPA (static Nginx image), TanStack Query. **UI language: English only** (unlike the Czech `home`/`fin` UIs — a deliberate exception for this internal ops tool).

**Screens**
- **Board** (`/`) — grid/list of sites, each a colored card (green/orange/red/grey-unknown) with name, last-check time, open-crash count, and rolling `uptime_pct` (or a "monitoring off" affordance when `uptime_pct` is null / `monitor_enabled` is false). Auto-refetch (e.g. every 30–60s). Filter by color.
- **Site detail** (`/sites/:id`) — uptime strip + latency percentiles (from `GET /api/sites/{id}/uptime`), crash groups list, config editor, "rotate ingest key" (shows key once in a modal), the ingest snippet (URL + key) to copy.
- **Add site** — form: id, name, monitor_url, options; on success shows the ingest key once.
- **Crash group** (`/crashes/:groupId`) — group header (count, level, first/last seen, status), event stream with stack/context, resolve/ignore actions.

**Data fetching (TanStack Query)**
- Query keys: `['sites']`, `['site', id]`, `['site', id, 'uptime', window]`, `['site', id, 'checks', cursor]`, `['site', id, 'crashes', filters]`, `['crashGroup', groupId]`.
- Invalidation: mutations (create/patch/delete site, rotate key, triage group) invalidate the relevant keys; board polls on an interval.
- **Empty state:** "No sites yet — add your first site." **Loading:** skeleton cards. **Error:** inline retry; distinguish 401 (redirect to auth login) from 5xx.

## 8. Non-Functional Requirements

- **Observability:** implements the baseline — `GET /healthz`, `GET /readyz` (with SQLite check), structured JSON logs to stdout, per-request logging (method, path, status, latency). Poller and purge log run summaries.
- **Performance:** tiny load — a handful of sites, one admin. Polling every 5 min with a 10s timeout. Ingest is the only potentially bursty path; protected by per-key rate limit + 64 KB body cap. Board reads are served from cached color + cached `uptime_pct` (never a raw-check scan); uptime detail comes from daily `check_rollup` rows, not raw checks.
- **Security:** ingest keys stored hashed, shown once, rotatable; ingest endpoint rate-limited and size-capped to blunt spam (public by design — browser keys are not secret, same trade-off as a Sentry DSN); all admin endpoints require auth; input validation on site id / URL / crash body; secrets via Coolify env only.
- **Backup:** Litestream → R2 under prefix `status/`. Fresh build restores initial load from Litestream/R2. Single DB file.

## 9. Configuration

All via Coolify env vars (no secrets in repo):

- `AUTH_BASE_URL` / `AUTH_SITE_ID=status` / auth shared config — dashboard auth against the `auth` backend (mirror `home`/`fin`).
- `SESSION_SECRET` — own session cookie signing.
- `CHECK_INTERVAL` (default `5m`), `CHECK_TIMEOUT` (default `10s`) — poller. Recommended `monitor_url` for own services: `/readyz`.
- `RED_FAIL_THRESHOLD` (default `2`) — consecutive failed checks before a site goes red (debounce).
- `UPTIME_WINDOW` (default `90d`) — rolling window for the board's cached `uptime_pct`.
- `RETENTION_DAYS` (default `90`) — raw-row purge window (`check_result`, `crash_event`).
- `ROLLUP_RETENTION_DAYS` (default `400`) — `check_rollup` retention (kept longer than raw checks).
- `MAX_INGEST_BYTES` (default `65536`), `INGEST_RATE` (default `60/min`, burst `120`) — ingest guards.
- `REOPEN_ON_REGRESSION` (default `true`) — reopen resolved groups on new events.
- `LITESTREAM_*` / R2 creds — backup (prefix `status/`).
- `PORT` — backend listen port: **112** (frontend static app: **155**). Home `7999`, fin `8999`.
- No BE→BE shared secret is required in v1 (ingest uses per-site keys, not `X-Service-Secret`).

## 10. Open Questions

_Resolved at review (2026-08-01):_ subdomain `status.tilcer.cz`; **Mode B** auth; backend port **112**, frontend **155**; repo **`ws-tilcer-status`**; **2-fail** red debounce (`RED_FAIL_THRESHOLD=2`); poll **`/readyz`** by default; ship docs + quick-start + copy-in Go/JS helpers; all-behind-auth (no public status page in v1).

Remaining minor tunables (defaults chosen; adjust anytime, not blockers):

1. **Orange window default** — 24h of recent crashes → orange. Shorten/lengthen?
2. **Ingest batching** — v1 accepts one event per request. Add batch ingest later if volume warrants?
3. **Ingest key for frontends** — public by design (Sentry-DSN model). Fine to keep, or restrict to backends later?

## 11. Acceptance Criteria

- [ ] Admin can add a site with a user-supplied id and receive an ingest key (shown once).
- [ ] Adding a duplicate id returns `409`; invalid id/url returns `422`.
- [ ] Poller checks every monitor-enabled site every 5 min (targeting `/readyz` for own services) and records results.
- [ ] A reachable site with no recent crashes shows **green**; reachable with a crash in the last `crash_window_hours` shows **orange**; a never-checked monitored site shows **unknown**.
- [ ] Red is debounced: a single failed check does **not** flip red; the site goes **red** only after `RED_FAIL_THRESHOLD` (2) consecutive failures, and recovers to green/orange on the next successful check.
- [ ] `POST /api/ingest/{siteId}` with a valid `X-Ingest-Key` returns `202` and creates/updates a crash group by fingerprint; wrong key returns `401`; unknown site `404`; oversized body `413`; over-rate `429`.
- [ ] Two similar crashes land in the same group and increment its count; a distinct message opens a new group.
- [ ] Resolving a group removes it from the site's orange signal.
- [ ] Deleting a site cascades its checks, rollups, groups, and events.
- [ ] `GET /api/sites/{id}/uptime` returns a rolling `uptime_pct`, latency p50/p95, and `buckets[]` for the strip, served from `check_rollup` (not a raw-check scan); a bucket with no checks reports `ok_pct: null`.
- [ ] The board card shows `uptime_pct` for monitored sites and a **"monitoring off"** affordance (not a percentage) when `monitor_enabled` is false or `uptime_pct` is null; a crash-only site is never red.
- [ ] The nightly rollup runs **before** the retention purge; after 90-day raw purge, 90-day uptime still renders from rollups.
- [ ] Retention purge removes raw check + crash rows older than 90 days and rollups older than `ROLLUP_RETENTION_DAYS`.
- [ ] `/healthz` and `/readyz` behave per baseline; `/readyz` fails when SQLite is unavailable.
- [ ] Dashboard renders board, site detail, crash group, and add-site flows with empty/loading/error states; 401 redirects to auth login.
- [ ] Litestream replicates to R2 prefix `status/`; a fresh build restores from R2.
- [ ] OpenAPI 3.1 spec matches the implemented surface.
- [ ] `README.md` quick-start exists and a new user can go from "add site" to "crash on the board" following only it, in a few minutes (includes a runnable `curl` example).
- [ ] Detailed integration docs cover payload fields, fingerprinting, environments/releases, rate limits (`429`) and size cap (`413`).
- [ ] Copy-in Go helper (`Report` + `Recover`) and browser JS snippet (`onerror`/`unhandledrejection`) are provided and verified to land a crash end-to-end; both fail safe (never crash the host app on ingest failure).
- [ ] Backend runs on port **112**, frontend on **155**; dashboard uses **Mode B** auth (self-hosted login + own session) against `auth` site `status`.
- [ ] Dashboard UI is **English only**.
