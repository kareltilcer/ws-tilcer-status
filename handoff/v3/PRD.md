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

## 12. V2 — As Built (§V2-12)

> Added **2026-09-02**, reconciled against repo `main` @ **`28013d0`** by reading the source; the live
> service was probed over HTTPS the same day. **Everything above this section is the pre-build spec and
> has not been edited. Where the two disagree, this section wins.**

### 12.1 Provenance

- The service is **built and live**. Five commits on a single `main`, **no PRs** (unlike `home`):
  `1d66811` handoff v1 · `cfc030b` handoff v2 · **`34d1e8a` "initial commit" — the entire
  implementation** (117 files, ~12.5k lines, 2026-08-02) · `0d103e1` + `28013d0`, a post-deploy
  routing fix.
- `handoff/v2/{PRD.md,openapi.yaml,HANDOFF-design.md,HANDOFF-engineering.md}` and the Nextcloud
  `services/status/` copies were **byte-identical** (md5-verified) before this section landed — there
  was no doc drift between them, but **neither had an as-built record**. The only as-built documents
  were `README.md` and `CLAUDE.md` in the repo.
- **Live probe, 2026-09-02:** the SPA serves at the origin; `GET /api/sites` → **401** (gated,
  correct); `GET /api/auth/session` → **401**; `GET /api/meta` → **404** (§12.11);
  `https://status.tilcer.cz/readyz` returns **the SPA shell**, not the probe (§12.11).

### 12.2 Structure — there are three modules, not two

§1 and HANDOFF §1 describe two functional modules over a "shared `sites` registry". As built,
**`internal/sites` is a full module** and **owns the entire database schema** — one migration,
`internal/sites/migrations/10001_init.sql`, creating all five tables of §5. `monitoring` and `crash`
return `Migrations() == nil` and own no tables of their own; they key off `site` and call into
`sites.RecomputeAndPersist`.

Two packages exist that the spec named only as behaviour: **`internal/scheduler`** (the poll ticker
plus a daily UTC wall-clock timer — no cron library; UTC has no DST so the timer never drifts) and
**`internal/retention`**. `internal/bootstrap` assembles the migration sequence.

HANDOFF §3's suggested tree was not followed. Actual layout: `backend/cmd/status/main.go`,
`backend/internal/platform/*` (not bare `internal/httpx`, `internal/store`), `frontend/` (not `web/`),
and the contract at **`backend/openapi.yaml`**, not the repo root.

### 12.3 Deploy & ports — the frontend is port 80

Backend **112**, as specified. **The frontend serves on port 80** inside its Nginx image; the
**"155" in §9, HANDOFF §2 and REGISTRY was never a container port**. Only the offline
`docker compose` harness binds a 155-ish number, mapping **1155 → 80**. The two Coolify apps are
`status-backend` (domain `status.tilcer.cz/api`, Dockerfile `/backend/Dockerfile`, health check
`/readyz`, volume `/data`) and `status-frontend` (catch-all domain, base directory `/frontend`).

### 12.4 Configuration — §9 is superseded by `README.md`

All service-owned config is **`STATUS_`-prefixed**, per the `home`/`fin` fleet convention. The
illustrative names in §9 **do not exist**:

| §9 said | As built |
|---|---|
| `AUTH_SITE_ID=status` | `STATUS_SITE_KEY` (default `status`) |
| `SESSION_SECRET` | **none** — sessions are random tokens stored **SHA-256-hashed** in `sessions` |
| `PORT` | `STATUS_ADDR` (default `:112`) |
| `CHECK_INTERVAL`, `RETENTION_DAYS`, … | `STATUS_CHECK_INTERVAL`, `STATUS_RETENTION_DAYS`, … |

⚠ **§9's "No BE→BE shared secret is required in v1" is wrong.** Mode B requires **both**
`STATUS_AUTH_SERVICE_SECRET` (the `X-Service-Secret` for auth's `/internal/*`) and
`STATUS_AUTH_JWT_SECRET` (the shared HS256 secret used to verify auth's minted tokens). Both are
**required** unless `STATUS_DEV_AUTH_BYPASS` is on. The ingest key model is unaffected — that part of
§9 stands.

Variables the spec never mentioned: `STATUS_ENV` (`development`|`production`; the dev bypass is a
hard startup failure when `production`), `STATUS_STATIC_DIR` (unset in the two-app deploy),
**`STATUS_TRUSTED_PROXY_COUNT`** (default **1** = Coolify's lone Traefik; governs X-Forwarded-For
client-IP resolution for access logs and login rate-limit keying — set **2** if a CDN fronts Traefik,
and note that setting it too high lets clients spoof their rate-limit key),
`STATUS_DEV_ACTOR_ID`/`_ROLES`, `STATUS_POLL_CONCURRENCY` (8), `STATUS_DAILY_JOB_AT` (`00:15` UTC),
`STATUS_ALLOWED_ORIGINS`, `STATUS_SESSION_TTL_DAYS` (90), `STATUS_ROLE_REFRESH_MINUTES` (15),
`STATUS_AUTH_JWT_ISSUER`.

Config **fails fast and enumerates every problem at once**, and cross-validates
`STATUS_ROLLUP_RETENTION_DAYS >= max(STATUS_RETENTION_DAYS, STATUS_UPTIME_WINDOW)` — a shorter rollup
retention would silently truncate the reported uptime window with no error.

### 12.5 Data model — a sixth table

§5's five tables are built exactly as specified, **plus one the spec omitted**: **`sessions`**
(platform migration `02001_sessions.sql`) — `id`, `user_id`, `token_hash` (SHA-256 of the cookie
token, unique), `email`, `display_name`, `roles` (JSON array cached from auth), `roles_refreshed_at`,
`user_agent`, `ip`, `created_at`, `last_seen_at`, `expires_at` (sliding), `revoked_at`. Mode B needs
it; §5 never listed it.

Migration blocks are ordered purely by the numeric filename prefix: **platform `02xxx`, sites
`10xxx`**. One index beyond §5: **`idx_crash_event_received`** on `crash_event(received_at)`, for the
purge. `foreign_keys` is a **DSN pragma** (per-connection), not a migration statement.

### 12.6 API surface — `openapi.yaml` does not describe the built service

⚠ **`backend/openapi.yaml` is byte-identical to `handoff/v2/openapi.yaml` and still reads
`version: 0.2.0`.** The build never updated it — the same failure as `home` v7/v8. Four live routes
are absent from the contract:

- `POST /api/auth/login` — Mode B login (public, pre-session).
- `GET /api/auth/session` — current session/identity.
- `POST /api/auth/logout` — CSRF-protected.
- **`GET /api/meta`** → `{ "uptime_window_days": <int> }`, added so the SPA labels the uptime figure
  with the real `STATUS_UPTIME_WINDOW` instead of hardcoding 90.

One documented parameter is also wrong: `/api/sites/{id}/uptime`'s `buckets` is specced
`default: 90`; as built the default is **per window** (§12.8).

The §6 auth description is superseded too: the browser carries **no bearer token**. Authorization is
the status **session cookie** (`credentials: 'include'`) plus a **double-submit CSRF token** from a
JS-readable `csrf` cookie on unsafe methods. `bearerAuth` never reaches the SPA.

### 12.7 Behaviour the spec left open, decided in the build

- **Reachability resets on a config change.** Changing `monitor_url` or `expected_status`, or
  re-enabling monitoring, sets `fail_streak = 0, last_ok = NULL, last_checked_at = NULL,
  cached_color = 'unknown'` — a streak evaluated against the *old* criterion must not colour the site
  under the new one. FR-3 said only "PATCH updates config".
- **A create/patch asymmetry, deliberately kept.** `POST /api/sites` with `monitor_enabled: true` and
  no `monitor_url` **silently coerces to disabled**; `PATCH` with the same input is a **422**.
  Locked in by `TestUpdateMonitoringContract`.
- **Colour never holds a stale orange.** FR-6 says a sub-threshold streak "keeps its prior colour".
  As built, `ComputeColor` returns `PriorColor` — *except* when the prior colour is orange, in which
  case it returns **green**: reaching that branch means the crash that coloured it orange has already
  aged out of the window.
- **Colour is computed on read** in list and detail (orange must age out by wall clock);
  `cached_color` is a **write-through fallback**, recomputed inside the same transaction as every
  check, ingest and triage. §8's "board reads are served from cached colour" is not literally what
  happens.
- **Roles.** §3 says "single admin, no roles to design for". The fleet role gate applies anyway:
  **any authenticated session may read**; **mutations require `admin`** (or the `*` superuser) —
  `POST/PATCH/DELETE /api/sites`, `rotate-key`, `PATCH /api/crashes/{groupId}`.
- **MFA is not handled in-app.** An auth MFA challenge surfaces as **409 `mfa_required`** and the
  login screen links out to `auth.tilcer.cz`. Google OAuth stays auth-hosted.
- **Ingest guard order is fixed** so an untrusted body is never parsed on a bad key:
  **404** unknown site → **401** bad key → **429** over-rate (with `Retry-After`) → **413** oversized
  → **422** invalid → **202**. Decoding is strict (`DisallowUnknownFields`, trailing content
  rejected). A future `occurred_at` is clamped to receipt time.
- **Ingest keys:** `ik_` + 32 random url-safe bytes, SHA-256 stored, constant-time compared.
- **Pagination:** keyset cursor over the fixed-width UTC timestamp; `limit` clamped **1..200**,
  default **50**; a malformed cursor is a **422**.

### 12.8 Uptime (FR-13) — the 24 h window is a deliberate raw scan

`window` is an enum **`24h|7d|30d|90d`**. **`7d`/`30d`/`90d` read only `check_rollup`**, as specified.
**`24h` performs a bounded raw `check_result` scan** (~288 rows/day at 5-minute polling), because
daily rollups cannot render an intraday strip. This knowingly departs from the §11 criterion "served
from `check_rollup` (not a raw-check scan)"; the scan is bounded by construction and the criterion
should be read as applying to the 7d/30d/90d views and the board figure.

`buckets` defaults to the window's natural granularity — **24 / 7 / 30 / 90** — not a fixed 90, which
left short windows mostly empty. Range 1..180. For non-24h windows the bucket grid is **snapped to
whole UTC days** (`to` becomes the start of tomorrow) so a day's rollup cannot straddle a boundary and
shift the strip's date labels by up to a day. A gap bucket reports `ok_pct: null`, as specified.

### 12.9 Frontend — no Tailwind, no shadcn/ui

§7 and the design brief's §8 ("reuse `home`/`fin` tokens") were satisfied visually but **not by reusing
their stack**. As built: **React 19 + Vite 8 + TypeScript 6 + react-router-dom 7 + TanStack Query 5 +
`sonner`** for toasts. There is **no Tailwind and no shadcn/ui**. Styling is **inline styles plus
oklch CSS custom properties** in `src/theme/globals.css` (every status colour ships both a fill token
and an AA-tuned `-text` token), with **Hanken Grotesk + IBM Plex Mono** via `@fontsource`.

**Dark is the default**, flipped by toggling a single **`.light`** class on `<html>` — there is no
`.dark` class — persisted in `localStorage['status-theme']`. Routes are `/`, `/add`, `/sites/:id`,
`/crashes/:groupId`. The board refetches every **30 s**; global `staleTime` is 10 s; 401/403 are never
retried and 401 hands off to the login screen. The 230 px side nav collapses to a drawer on mobile.

### 12.10 §11 acceptance criteria — where each stands

**Covered by tests** (34 test functions across 11 files; `internal/apitest` drives the real router over
HTTP with the dev bypass): site create + show-once key, duplicate `409`, invalid `422`, ingest `202`
with group upsert, similar-crash grouping, wrong key `401`, unknown site `404`, oversized `413`,
rate-limit `429`, resolve removes the group from the orange signal, reopen-on-regression, browse +
drill-in, **cascade delete**, crash-only site never red, the 2-fail debounce **and recovery**
(`TestPollerDebounceAndRecovery`), rollup → `uptime_pct` + p50/p95, day-aligned buckets, per-site
rollup flagging, monitoring-off uptime, **purge boundaries**, fingerprint normalisation/grouping/
client override, key generation + constant-time compare, cursor validation, and the config
cross-validation.

**Verified by reading the source, not by a test:** the rollup-runs-before-purge ordering (composed in
`main.go`, driven by `scheduler.Jobs.Daily`); Litestream restore-on-boot; the FR-12 deliverables.

**Not met.** "OpenAPI 3.1 spec matches the implemented surface" — see §12.6. "Backend runs on port
112, frontend on **155**" — see §12.3. `/healthz` and `/readyz` "behave per baseline" is true of the
binary but not of the public origin — see §12.11.

**Unverified from the repo.** The client helpers are shipped and fail-safe by construction, but there
is no end-to-end test proving each lands a crash. The design brief's §12 deliverables **5**
(`design:accessibility-review` report) and **6** (`design:design-handoff` redline) are not in the
repo — only `design/v2/status-handoff-v2.zip` (`Status.dc.html`).

### 12.11 Known defects, live

**1. `GET /api/meta` returns 404 in production.** Commits `0d103e1`/`28013d0` added
`httpx.StripAPIPrefix`, a defensive path normaliser that tolerates *both* proxy misconfigurations:
`/api/api/*` → `/api/*`, and a **stripped** prefix re-prefixed via an allow-list
`{"/auth/", "/sites", "/crashes", "/ingest/"}`. Health probes are never rewritten.

**`/meta` is not on that allow-list.** The live split — `/api/sites` → 401 but `/api/meta` → 404 — is
the signature of **Strip Prefix being *enabled* in Coolify**, the opposite of what `README.md` and
`CLAUDE.md` instruct: with the prefix stripped, `/sites` is re-prefixed and matches while `/meta`
falls through to the router's JSON 404. `TestStripAPIPrefix` enumerates exactly the four allow-listed
prefixes and never tests `/meta`, which is why the gap shipped.

The SPA degrades silently (`meta?.uptime_window_days ?? 90`), so the board labels uptime **"90 d"
regardless of `STATUS_UPTIME_WINDOW`** — precisely the drift `/api/meta` was added to prevent. Fix:
add `"/meta"` to the allow-list (and a test case), **or** turn Strip Prefix off in Coolify. Doing both
is cheapest.

**2. The health probes are unreachable at the public origin.** Traefik routes only `/api` to the
backend, and `StripAPIPrefix` deliberately refuses to rewrite `/healthz` and `/readyz`, so
`https://status.tilcer.cz/readyz` serves **the SPA shell**. Coolify's container health check hits
:112 directly and is unaffected, but **`status` cannot monitor itself through its own public URL** —
and FR-11's "these are also natural `monitor_url` targets for other services" does not hold for this
service's own origin.

### 12.12 Owed

1. Fix `GET /api/meta` (§12.11 defect 1) — one allow-list entry plus a test case.
2. Decide and document whether Strip Prefix is on or off in Coolify, then make the docs and the
   middleware agree.
3. Bring `backend/openapi.yaml` to the built surface (the three `/api/auth/*` routes, `/api/meta`, the
   per-window `buckets` default, cookie+CSRF auth instead of `bearerAuth`) and bump past **0.2.0**.
4. Update the `status` row in `REGISTRY.md` — it still reads "spec draft … not implemented".
5. Run the design brief's owed §12.5 accessibility review and §12.6 engineering redline, or record
   that they were dropped.
6. Consider an end-to-end test for the Go and JS helpers (§11's last unverified criterion).

---

# V3 — Feedback (§V3-1 … §V3-11)

> **Spec, written 2026-09-02** against the frozen scope in `V3-feedback-brief.md`
> (decisions **V3-D01–V3-D49**). Companion contract: `openapi.yaml` **0.3.0**.
> **Nothing here is built.** §V3-12 is reserved for the as-built reconciliation and,
> when it exists, wins over everything in this block — the same rule §12 established
> for v2.
>
> This block adds a **fourth module**, `feedback`. Sections mirror §1–§11 of the v2
> spec; functional requirements continue the existing numbering at **FR-14**.
>
> ⚠ **Three decisions were made while writing this document, against the repo rather
> than in the interview.** They are stated where they land and collected in §V3-10:
> **V3-D50** dissolves the brief's one open modelling question, **V3-D51** moves the
> per-site config onto its own routes, and **V3-D52** gives `feedback` two mount
> points because the module registry only hands out the authenticated router.

## §V3-1 Overview

- **One-line summary:** the users of a monitored app can report a problem from
  inside it — a button and a dialog, with screenshots or a short video — and Karel
  triages what arrives in one inbox.
- **Modules after v3:** `sites`, `monitoring`, `crash`, **`feedback`**.
- **New infrastructure:** one Cloudflare R2 bucket, `ws-tilcer-status-feedback`,
  private, in the same account as the Litestream bucket and reached with **its own
  token scoped to it alone** (V3-D19, V3-D21).
- **New public surface:** three routes under `/api/ingest/{siteId}/feedback*`,
  authenticated by a per-site **widget key** (`wk_`), and a versioned JavaScript
  bundle at `/widget/v1.js` served by the frontend image.
- **Deliverables beyond the running service:**
  1. `frontend/src/widget/` → `/widget/v1.js`, a framework-free bundle in a shadow root.
  2. `docs/widget.md` — the one-line embed, the key flow, and what the widget sends.
  3. **`openapi.yaml` 0.3.0**, reconciled against the built surface (§V3-6).
  4. **The CORS middleware v2 never had** (FR-25) — a repair, not a new feature.
  5. The `StripAPIPrefix` correction (§V3-8).

**What v3 does not change.** Colour. `ComputeColor` is untouched, and a test says so
by name. Reports are counted on the board, never coloured into it: a person saying
"this is confusing" must not make `home` look degraded beside a real outage.

## §V3-2 Goals & Non-Goals

**Goals**

- A household member hitting a bug in `home` can say so in under thirty seconds,
  from inside `home`, without knowing that status exists.
- What arrives is worth reading: the text, what page they were on, what the browser
  was, and a picture or a clip.
- One queue. Five apps, one inbox, four states.
- Nobody can fill a paid bucket from a browser.
- The reporter gets a code they can quote, and nothing else — no account, no thread,
  no notification.

**Non-Goals (v3)**

- No reply route, no email, no push. The reporter's only feedback is the confirmation.
- No public bug tracker and no unauthenticated read surface of any kind.
- No anonymous reporting: v3 is authenticated apps only, and `karel.tilcer.cz` — public
  and account-less — waits for v4.
- No in-browser screenshot or screen capture; the user attaches a file they made.
- No transcoding, thumbnails, poster frames, or ffmpeg anywhere on the droplet.
- No attachment backup, mirroring or versioning (§V3-8).
- No link between a report and a crash group.
- No per-site storage accounting; `home`'s Úložiště has no counterpart here.
- No translation of the status admin UI — the widget is the only translated surface.
- No captcha.

## §V3-3 Users & Roles

- **Admin (Karel)** — unchanged. Reads everything; mutations require `admin` via
  `httpx.RequireAdmin`, exactly as `POST /api/sites` and `PATCH /api/crashes/{id}` do.
- **The reporter (new)** — a human using a monitored app. **status has no identity for
  them and issues them nothing.** The host app may pass a display hint
  (`reporter_label`); it is an untrusted string, escaped and shown, never joined to
  anything and never authenticated.
- **The widget (machine)** — authenticated only by the per-site widget key. It can
  submit a report and confirm its own uploads. It can read nothing.

⚠ **The widget key is not a boundary.** It is rendered into HTML that any signed-in
member can read, so a household member can lift it and post from anywhere. Mounting
the widget only for signed-in users is friction, not a control. What actually bounds
the damage is the rate limits, the kill switch, and the fact that an upload URL only
exists inside an accepted report (§V3-8).

## §V3-4 Functional Requirements

### FR-14: Enable feedback for a site
- **Trigger:** `PATCH /api/sites/{id}/feedback-config` with `{"enabled": true}`.
- **Behaviour:** creates the `feedback_site_config` row if absent, generating a widget
  key (`wk_` + 32 random url-safe bytes) and storing **only its SHA-256**. The
  plaintext is returned once and never again — the `ik_` precedent exactly. A site with
  no row has feedback off; **absence is the default state, not a missing row to repair**
  (V3-D03).
- **Outputs:** `200` with the config and, on first enable, the plaintext key.
- **Errors:** `404` unknown site · `403` CSRF/origin · `422` · **`503`** when the
  deployment has no object storage configured (`STATUS_FEEDBACK_ENABLED=false`) — the
  switch must not be flippable into a state the process cannot serve.

### FR-15: Rotate the widget key
- **Trigger:** `POST /api/sites/{id}/rotate-widget-key`.
- **Behaviour:** new key, new hash, plaintext once, old key dead immediately.
- ⚠ **It must not disturb crash ingest.** The two keys are separate precisely so that a
  spammed widget can be revoked without silencing that site's crash reporting. A test
  rotates the widget key and asserts a crash still ingests with the unchanged `ik_`.

### FR-16: Serve the widget's configuration (public)
- **Trigger:** `GET /api/ingest/{siteId}/feedback/config`, header `X-Widget-Key`.
- **Behaviour:** returns `enabled`, the caps, the content-type allow-list, whether
  console capture is on, and a **single-use signed ticket**.
- **The widget renders nothing until this answers.** A disabled site, an unknown key or
  a network failure means **no launcher button at all** — never a button that fails when
  pressed (V3-D35).
- **The ticket** (V3-D31) carries an id, an issue time and an HMAC over both under
  `STATUS_FEEDBACK_TICKET_SECRET`. Submission requires it. This is what makes the dwell
  check meaningful: a `dwell_ms` field sent by the client is a number the client chooses,
  whereas the ticket's issue time is signed by the server. It also rate-limits dialog
  *opens*, not merely submissions.

### FR-17: Submit a report (public)
- **Trigger:** `POST /api/ingest/{siteId}/feedback`, header `X-Widget-Key`.
- **Inputs:** `message` (≤4 000 chars), `kind`, `ticket`, the optional context fields,
  the honeypot `website`, and up to `MAX_FILES` **declared** files as
  `{content_type, byte_size}`.
- **Guard chain — a fixed order, enumerated by a test** (V3-D16):

  **404** unknown site → **401** bad widget key → **403** feedback disabled → **403**
  origin not allowed → **429** over rate (key or IP) → **413** body over
  `MAX_TEXT_BYTES` → **422** invalid → **202**.

  The v2 principle carries over unchanged: **an untrusted body is never parsed before the
  key is checked.** Disabled-and-known is a `403` rather than a `404` because the caller
  has already proved they hold the key — there is nothing left to conceal, and a `404`
  would send Karel debugging a site id that is correct.
- **`422` covers:** bad, expired, spent or wrong-site ticket · honeypot non-empty ·
  dwell below `MIN_DWELL_MS` · a content type outside the allow-list · more than
  `MAX_FILES` · empty message.
- **Behaviour:** inserts the report with a fresh `ref`, hashes the client IP with
  `STATUS_IP_HASH_SALT` (**the IP itself is never written to a row**), inserts one
  `pending` attachment row per declared file, and mints one presigned PUT per row.
- **Outputs:** `202 {ref, uploads[]}`.

### FR-18: Upload the attachments
- **Trigger:** the browser PUTs each file to the URL it was given.
- ⚠ **Upload URLs exist only inside the response to an accepted report** (V3-D07).
  There is no standalone "give me an upload URL" endpoint. No accepted report means no
  way to write a byte into the bucket, and that single structural fact does more against
  abuse than every rate limit combined.
- ⚠ **The presigned PUT signs `Content-Type` and `Content-Length` as signed headers**, at
  the exact declared size, clamped server-side to the cap before signing (V3-D08). This is
  the only size enforcement a presigned PUT can have. A URL signed for 4 194 304 bytes
  refuses 4 194 305, and refuses it **at R2** — the droplet never sees the bytes. A client
  that under-declares receives a URL that will not accept the file it actually holds.

- ✅ **V3-D54 — V3-D08 HOLDS. Measured against the real bucket, 2026-09-02.**

  It could not be settled from documentation — Cloudflare states plainly that presigned
  POST is not supported on R2, so the standard `content-length-range` policy does not
  exist here, and whether R2 enforces a *signed* `Content-Length` on a PUT is documented
  nowhere. `services/status/spike-r2-presign.py` asked R2 directly. Five probes:

  | # | Probe | Result |
  |---|---|---|
  | 0 | Does the SDK sign it? | `content-length;content-type;host` — **yes** |
  | 1 | Signed 1 KiB, sends 1 KiB | **200**, 1 024 bytes stored |
  | 2 | Signed 1 KiB, declares and sends 64 KiB | **403 SignatureDoesNotMatch** |
  | 3 | Signed 1 KiB, **declares 1 KiB, sends 64 KiB** | **200 — and 1 024 bytes stored** |
  | 4 | Nothing signed but the type, sends 64 KiB | **200**, 65 536 bytes stored |
  | 5 | Signed `image/png`, sends `video/mp4` | **403 SignatureDoesNotMatch** |

  **The bucket cannot be filled beyond the signed size.** Probe 2 is refused outright, and
  probe 3 — the actual attack — stores exactly the signed number of bytes.

  ⚠ **But the mechanism is truncation, not refusal, and the difference is load-bearing.**
  R2 reads exactly `Content-Length` bytes off the wire, discards the rest, and answers
  **200**. A client that lies gets a success response and a corrupt object of exactly the
  right length. Three consequences, each recorded rather than left implicit:

  1. **The abuse model is intact.** An attacker cannot exceed the cap, cannot be told they
     succeeded in exceeding it, and still needs an accepted report per upload (V3-D07).
  2. **The claim step's size check is a presence check, not an integrity check** — see the
     correction in FR-19. An object that matches its declared length may still be the first
     1 KiB of a 64 KiB file.
  3. **Content type is enforced too** (probe 5), so V3-D09's allow-list is a real
     constraint at R2 rather than advisory.

  ⚠ **Probe 4 is the one to remember.** Drop `ContentLength` from the presign call — as a
  "simplification", or through an SDK change — and the bucket becomes an open upload
  endpoint with no error anywhere. That is why §V3-11 requires a test asserting
  `content-length` appears in `X-Amz-SignedHeaders` **before** any upload is attempted.

  **Not adopted: a signed checksum.** Signing `x-amz-checksum-sha256` would turn
  truncation into refusal, since a short body would fail verification. It is declined: it
  costs a full client-side hash of a 50 MB video before the upload starts, and it defends
  only against a reporter deliberately corrupting their own bug report. The corruption case
  is unreachable by an honest client, whose declared size is `file.size` for the very File
  it then sends. Revisit only if a real truncated attachment ever appears.
- `content_type` comes from a **fixed allow-list** — `image/png`, `image/jpeg`,
  `image/webp`, `image/gif`, `video/mp4`, `video/webm` — matched against what the client
  declares, **never inferred from the filename**, and the extension in the object key is
  derived from the allow-listed type rather than from user input (V3-D09).
- Object key: `feedback/{site_id}/{ref}/{n}-{rand}.{ext}`.
- Uploads run **sequentially** (V3-D38): a 50 MB video and two screenshots in parallel on
  household wifi is worse than in series. A failed PUT is retried once, then abandoned.

### FR-19: Claim the uploads (public)
- **Trigger:** `POST /api/ingest/{siteId}/feedback/{ref}/claim`, same key.
- **Behaviour:** `HEAD`s every `pending` object and records **the size R2 reports, not the
  size the client declared** (V3-D10). Absent or wrong size ⇒ `missing`.
- ⚠ **What this check can and cannot establish, given V3-D54's finding.** R2 truncates a
  lying upload to the signed length and answers 200, so an object whose HEAD matches its
  declared size is **present and within the cap — and nothing more**. It may be the first
  1 KiB of a 64 KiB file. The check is therefore a **presence and cap check, not an
  integrity check**, and the code should say so where it runs. The remaining detectable
  failures are the ones that matter operationally: the object is absent (never uploaded, or
  the PUT was refused for a signature mismatch), or it is smaller than declared (the client
  stalled mid-body). Both become `missing`.
- **Not verified, and benign:** declaring *more* than is sent. R2 waits for the missing
  bytes and the request fails or times out, leaving no object — which the claim step reads
  as absent and marks `missing`. No separate handling.
- ⚠ **A report is never rejected because an attachment failed.** The text is the thing
  worth keeping; the dashboard states that a file is missing rather than rendering a
  broken image.
- Idempotent, scoped to the named `ref`, and safe to omit — a report whose claim never
  arrives keeps `pending` rows until the sweep resolves them.

### FR-20: The cross-site inbox
- **Trigger:** `GET /api/reports?state=&site=&kind=&limit=&cursor=`.
- **Behaviour:** one queue across every site, newest first, keyset cursor over
  `(created_at, id)`, limit clamped 1..200 default 50 — the v2 pagination convention
  unchanged.
- ⚠ The cursor depends on `timeutil.Layout` making **string comparison a valid time
  order**. Never `time.RFC3339Nano`.

### FR-21: Triage a report
- **Trigger:** `PATCH /api/reports/{ref}` — `state`, `internal_note`, `kind`.
- **States:** `new → open → resolved | declined`. `resolved_at` is stamped on the two
  terminal states and cleared if a report is reopened.
- `internal_note` is admin-only and has no route that could ever show it to a reporter,
  because no such route exists.
- Requires `admin`.

### FR-22: Delete a report, and cascade a site
- **Trigger:** `DELETE /api/reports/{ref}`, or `DELETE /api/sites/{id}`.
- ⚠ **`ON DELETE CASCADE` removes rows. It cannot remove objects from R2.**
- **Order is normative** (V3-D05): collect the object keys **inside** the transaction,
  commit, **then** issue the R2 deletes. The reverse order destroys the attachments of a
  report that still exists if the commit fails.
- A delete that fails leaves an orphan, and the sweep is the backstop. The response does
  not wait on R2.
- ⚠ **No R2 call happens inside a transaction or while an outer `rows` cursor is open**
  (V3-D05a). The service runs `SetMaxOpenConns(1)`; a network round-trip inside `WithTx`
  holds the only writer connection for the length of someone else's TCP timeout. This is
  the easiest way to turn a feature about screenshots into an outage.

### FR-23: The nightly feedback sweep
- **Trigger:** the existing daily job at `STATUS_DAILY_JOB_AT`, which today composes
  **rollup → purge**. It gains a third step, **last**: **rollup → purge → sweep**
  (V3-D25). The ordering is deliberate — the sweep is the only step that talks to the
  network, and it must not be able to delay the two that keep the database honest.
- **Two actions** (V3-D26): mark `pending` attachments older than
  `STATUS_FEEDBACK_UNCLAIMED_TTL` as `missing` and delete their objects; then list the
  `feedback/` prefix and delete objects older than that TTL which match no live row.
- ⚠ **The sweep aborts on any listing error and deletes nothing** (V3-D27). A listing
  that comes back empty because of a credential error, followed by "delete everything
  with no live row", is how a bucket is quietly emptied. It deletes only under the
  `feedback/` prefix and never anything younger than the TTL — a GC that can outrun an
  in-flight upload is a data-loss bug wearing a maintenance-job costume.
- ⚠ **Retention does not apply to reports.** `RETENTION_DAYS` purges `check_result` and
  `crash_event`; a report is a hand-written artifact and is kept until deleted.
  `TestRetentionDoesNotPurgeFeedback` exists because the purge is a natural place for
  someone to add a fourth table by symmetry (V3-D28).

### FR-24: The embeddable widget
- **Delivery:** a **second Vite entry**, built as an IIFE to `/widget/v1.js`, served by
  the Nginx frontend image. **No React, no framework** — vanilla DOM in a **closed shadow
  root**, target under 15 kB gzipped. The SPA's React 19 is not a dependency the monitored
  apps should inherit, and the widget must survive being embedded in a page that already
  has a different React.
- **Embed:**
  ```html
  <script src="https://status.tilcer.cz/widget/v1.js"
          data-site="home" data-key="wk_…" data-lang="cs" data-reporter="Kája" defer></script>
  ```
  `data-lang` falls back to `document.documentElement.lang`, then to `cs`.
- **V3-D55 — the widget looks like nothing in particular, deliberately.** Its own quiet
  visual language: a light surface, one neutral accent, one radius, a system font stack.
  It is **not** status-branded — a dark oklch panel dropped onto `home`'s light Tailwind
  pages reads as something broken rather than as something belonging to another service —
  and it is **not** host-themeable, because a `--sfb-*` variable contract is design
  surface to specify, document and support for three apps styled by one person. Neutral
  and self-contained sits acceptably on `home`, on `fin` and on status's own dark board
  without pretending to belong to any of them, and it reads as what it is: a reporting
  tool that arrived with the page.
- **V3-D56 — a built-in launcher *and* a programmatic trigger.** By default the widget
  renders its own floating button (`data-position` to move it). It also exposes
  **`StatusFeedback.open()`**, so a host app can trigger the dialog from its own menu item
  and pass `data-launcher="none"` to suppress the floating one. One small public API, and
  it is what lets `home` put reporting in the nav where household members will actually
  find it rather than in a corner competing with whatever already lives there. The API is
  part of the `v1` contract: it may gain arguments, never lose them.
- **Versioning:** `/widget/v1.js` is `Cache-Control: public, max-age=31536000, immutable`;
  `/widget/latest.js` is a 302 to the current major with `max-age=300`. A breaking change
  becomes `/widget/v2.js` and every existing embed keeps working.
- ⚠ **Two specifics from `frontend/nginx.conf` as it stands.** A regex block matches every
  `.js` path and serves it from root with no fallback, so a missing `/widget/v99.js`
  already 404s correctly rather than returning the SPA shell — **keep the `.js` extension;
  never an extensionless widget path.** That same block stamps `expires 1y` and
  `Cache-Control: public, immutable` on **every** `.js`, `/widget/latest.js` included,
  which would pin "latest" for a year and defeat its purpose. Nginx matches an exact `=`
  location before any regex, so **`location = /widget/latest.js` must be declared
  explicitly** with the 302 and `max-age=300`.
- **Language:** Czech and English string sets ship **inside the bundle** — no runtime
  translation fetch. `strings_version` in the config response exists only so a mismatch is
  diagnosable. The admin UI stays English; the widget is this service's first and only
  translated surface.
- **Accessibility:** focus trap, `Escape` closes and restores focus to the launcher, full
  keyboard reachability, visible focus rings. This has more non-technical users than any
  other surface in the fleet.
- ⚠ **The widget never throws into the host app** (V3-D37). Every entry point is wrapped;
  every failure is silent except the dialog's own error state. v2 set this rule for the
  crash clients, and a widget that crashes `home` while reporting a bug in `home` would be
  a small masterpiece.
- **Console tail** is collected only when the site opted in, capped at 50 lines × 200
  chars, and **the dialog shows the reporter what will be sent before they submit**. A
  person who can see what they are attaching can decline to attach it.
- ⚠ **V3-D58 — the host app's Content Security Policy is part of the integration, and it
  is the requirement most likely to be missed.** A host that sends a CSP must allow three
  things, and the third is the trap:
  - `script-src https://status.tilcer.cz` — the bundle;
  - `connect-src https://status.tilcer.cz` — config, submit, claim;
  - `connect-src https://<account>.r2.cloudflarestorage.com` — ⚠ **the PUT goes to R2
    directly, not through status**, so the host's policy must name the bucket endpoint by
    origin. A site that allows only the status origin gets a widget that opens, accepts a
    file, and fails at upload with a console error the reporter will never see.

  ✅ **Measured 2026-09-02: no site in the fleet sends a CSP, so this blocks nothing
  today.** Checked at both layers — the repos carry no `add_header
  Content-Security-Policy` in any `nginx.conf` and no `<meta http-equiv>`, and a live
  same-origin fetch at `home.tilcer.cz` and `fin.tilcer.cz` returns no
  `Content-Security-Policy`, no report-only variant, and in fact **no security headers at
  all** (the only headers present are Cloudflare's and the cache/content set).

  ⚠ The requirement is therefore **forward-looking, and it is now a tripwire**: the day
  anyone adds a CSP to `home`, `fin` or `karel` — a reasonable thing to want — the widget
  breaks in a way that produces a console error the reporter will never see and no
  server-side signal at all. All three directives belong in `docs/widget.md` so that
  whoever adds a policy has the list in front of them.

  (`home` does send `Content-Security-Policy: sandbox` on served images, PDFs and chat
  content. That is a per-response sandbox on a served file, unrelated to the document's
  script/connect policy, and it does not affect the widget.)

### FR-25: Cross-origin access for the public endpoints — repairing v2
- ⚠ **status has no CORS handling at all**, verified against `main` on 2026-09-02.
  `httpx.NewRouter` mounts exactly `StripAPIPrefix`, `RequestID`, `Logger`, `Recover`.
  No `Access-Control-*` header is written anywhere in the backend and there is no
  `OPTIONS` handler. The only three mentions of CORS in the repository are frontend
  comments explaining that the SPA is same-origin and therefore does not need it — true
  of the dashboard, false of the deliverable beside it.
- **Consequence.** `clients/js/status-report.js` sends `Content-Type: application/json`
  **and** `X-Ingest-Key`; neither is CORS-safelisted, so every call is preflighted, the
  `OPTIONS` matches no route, falls to `r.NotFound`, and returns a JSON 404 with no
  `Access-Control-Allow-Origin`. **The browser blocks the POST.** It fails invisibly
  because the client is deliberately fail-safe (`/* fail safe: drop ingest errors */`).
  **§11's criterion "browser JS snippet … verified to land a crash end-to-end" is
  therefore false, not merely untested**, unless the page was served from
  status.tilcer.cz itself. The Go helper is unaffected — it is not a browser.
- **Behaviour:** a CORS middleware on the **public group only** — `/api/auth/*` and
  `/api/ingest/*` (V3-D42). The gated group stays same-origin: the dashboard is served
  from the status origin and nothing else may reach it with credentials.
- ⚠ **`Access-Control-Allow-Credentials` is never sent** (V3-D43). These endpoints
  authenticate by key, not by cookie; sending it would be the difference between a public
  ingest endpoint and a cross-origin door into a session.
- `Vary: Origin` on every response, matched or not.
- **A test posts a preflight from an allow-listed origin and from a foreign one and
  asserts the headers on both** (V3-D45). Nothing about this bug would be caught by a
  same-origin suite, which is exactly why it survived a build with 34 test functions.

### FR-26: View an attachment
- **Trigger:** `GET /api/reports/{ref}/attachments/{attachmentId}/url`.
- **Behaviour:** mints a presigned GET valid for `STATUS_FEEDBACK_VIEW_TTL` (default 5
  minutes). The bucket is private and has no public base URL or custom domain, so this is
  the only way an attachment is ever seen (V3-D20).
- **Why presigned rather than proxied:** R2 serves the range requests, so a 50 MB clip
  seeks correctly without the service implementing ranges itself — and no attachment byte
  passes through the droplet in either direction.
- ⚠ **The URL is a bearer token for its lifetime.** Anyone it is forwarded to can open it
  until it expires. Five minutes, and the dashboard never places one in a shareable link.
- **Errors:** `404` for an unknown report, an unknown attachment, **or an attachment not in
  `stored` state** — a `pending` or `missing` object has no URL to mint and must not
  produce a signed link to nothing.

## §V3-5 Data Model

**`feedback` is the first status module that owns a migration block.** As built, `sites`
owns the entire schema — one migration, all five tables — and `monitoring` and `crash`
return `Migrations() == nil`. That was defensible with two modules over one registry and
stops being defensible when a third functional module needs four tables: `sites` would be
carrying the schema of three modules it does not otherwise know about.

**V3-D01.** `feedback` declares migrations in block **`20xxx`**, joining `platform`
(`02xxx`) and `sites` (`10xxx`). `bootstrap.MigrationSources()` gains
`{Name: "feedback", FS: feedback.MigrationsFS}`; ordering remains purely the numeric
filename prefix.

**V3-D02.** `feedback` adds **no column to `site`**. Its per-site configuration is its own
table. A module that reaches into another module's table to add a column has not been
separated from it.

**`feedback_report`** — indexes `(site_id, created_at DESC)`, `(state, created_at DESC)`, unique `(ref)`

| col | type | notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | internal |
| `ref` | TEXT NOT NULL UNIQUE | `R-` + 4 Crockford base32 chars (no I/L/O/U) — shown to the reporter, used by every route |
| `site_id` | TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE | |
| `kind` | TEXT NOT NULL DEFAULT 'bug' | `bug` \| `idea` \| `other` |
| `message` | TEXT NOT NULL | ≤4 000 chars, stored as text, **never rendered as HTML** |
| `state` | TEXT NOT NULL DEFAULT 'new' | `new` \| `open` \| `resolved` \| `declined` |
| `reporter_label` | TEXT NULL | untrusted display string from the host app |
| `page_url` · `referrer` · `user_agent` · `viewport` · `locale` | TEXT NULL | page context |
| `app_release` | TEXT NULL | mirrors `crash_event.release` |
| `console_tail` | TEXT NULL | JSON array; written only when the site opted in |
| `last_error` | TEXT NULL | same opt-in |
| `internal_note` | TEXT NULL | admin-only |
| `ip_hash` | TEXT NULL | SHA-256 of client IP + `STATUS_IP_HASH_SALT`. **The IP is never stored** |
| `created_at` · `updated_at` | TEXT NOT NULL | `timeutil.Layout` |
| `resolved_at` | TEXT NULL | |

**`feedback_attachment`** — indexes `(report_id)`, `(state, created_at)`, unique `(object_key)`

| col | type | notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | |
| `report_id` | INTEGER NOT NULL REFERENCES feedback_report(id) ON DELETE CASCADE | |
| `object_key` | TEXT NOT NULL UNIQUE | `feedback/{site_id}/{ref}/{n}-{rand}.{ext}` |
| `content_type` | TEXT NOT NULL | from the signed allow-list |
| `byte_size` | INTEGER NOT NULL | declared at init, **overwritten with what HEAD reports** at claim |
| `state` | TEXT NOT NULL DEFAULT 'pending' | `pending` \| `stored` \| `missing` |
| `created_at` | TEXT NOT NULL | |
| `claimed_at` | TEXT NULL | |

**`feedback_site_config`** — PK `(site_id)`

| col | type | notes |
|---|---|---|
| `site_id` | TEXT PK REFERENCES site(id) ON DELETE CASCADE | |
| `enabled` | INTEGER NOT NULL DEFAULT 0 | the kill switch — off until deliberately turned on |
| `widget_key_hash` | TEXT NOT NULL | SHA-256, constant-time compared, plaintext shown once |
| `widget_key_set_at` | TEXT NOT NULL | |
| `console_capture` | INTEGER NOT NULL DEFAULT 0 | opt-in (§V3-8) |
| `created_at` · `updated_at` | TEXT NOT NULL | |

⚠ **There is no `allowed_origins` column** — see V3-D50 below.

**`feedback_ticket`** — PK `(id)`, index `(expires_at)`. Single-use submission tickets;
rows are deleted on use and swept by the daily job.

**Derived, not stored.** `SiteSummary.open_reports` is the count of `new` reports per
site. It is **not** a column on `site` and it is **not** part of `color`.

**V3-D04.** The count crosses a module boundary downwards. `sites` may not import
`feedback`, so `sites` defines a one-method provider interface —
`ReportCounts(ctx, siteIDs) map[string]int` — `feedback` implements it, and
`internal/bootstrap` wires it at composition. **Never a package-level global**: home's
§V5-12 correction, applied here from the start.

**V3-D53.** With no provider registered the field is **`null`, not `0`**, and the card
renders no badge. Zero is a claim that there is nothing to read; null is the truth, which
is that nobody asked.

## §V3-6 API Surface

Full detail in `openapi.yaml` **0.3.0** — 22 paths, 32 schemas, validated against the
OpenAPI 3.1 schema with every local `$ref` resolved.

**0.3.0 also repairs the contract itself.** V3-D18 required it to describe the *built*
service, so beyond v3's own routes it adds the four v2 routes the document never
mentioned — `POST /api/auth/login`, `GET /api/auth/session`, `POST /api/auth/logout`,
`GET /api/meta` — **removes `bearerAuth` entirely** in favour of `sessionCookie` +
`csrfToken` (the SPA has never sent a bearer JWT; a scheme nobody implements is worse
than no scheme), corrects `buckets` to default **per window** rather than to a fixed 90,
and re-describes `serviceSecret` as outbound-only. The v7/v8/v2 failure does not get a
fourth outing.

**Auth per group**

- `/healthz`, `/readyz` — public.
- `POST /api/auth/login`, `GET /api/auth/session` — public (login must work before a
  session exists).
- `POST /api/ingest/{siteId}` — public, `X-Ingest-Key`.
- `GET|POST /api/ingest/{siteId}/feedback*` — public, **`X-Widget-Key`**.
- Everything else — `session` cookie, and `X-CSRF-Token` on every unsafe method.

**V3-D12 — the public half hides under `/api/ingest/` on purpose.** That prefix is already
on `StripAPIPrefix`'s re-prefix allow-list and demonstrably works in production, where
`/meta` does not. The reporting path is therefore the half least likely to be broken by a
Coolify setting. This is defensive, not a substitute for the fix in §V3-8.

**V3-D51 — feedback configuration gets its own routes.** The brief said
`PATCH /api/sites/{id}` would grow feedback fields. It should not: that route belongs to
the `sites` module, and `feedback` owning config it cannot serve is the same boundary
violation as V3-D02 in a different costume. Instead:
`GET|PATCH /api/sites/{id}/feedback-config`, mounted by `feedback`.

**V3-D52 — `feedback` registers two mount points.** `registry.Module.RegisterRoutes(r)`
receives the **authenticated** router only; `crash` therefore mounts its public ingest
through the separate `MountPublicAPI` hook in `httpx.Deps`. `feedback` mirrors that shape
exactly — `RegisterRoutes` for the inbox and the config, a second method for the three
widget routes. A module with public routes is not a new pattern here; it is `crash`'s.

## §V3-7 Frontend

**Two independent artifacts from one repository.** The dashboard screens and the widget
share a build and nothing else — not a framework, not a stylesheet, not a token.

**Dashboard (English, status's own stack).**

- **Inbox** (`/reports`) — the cross-site queue. Filter by state, site, kind. Each row:
  site, kind, first line, reporter label, relative time, attachment count. Empty state:
  "No reports yet."
- **Report detail** (`/reports/:ref`) — the full text, the context block, the attachments
  (image inline, `<video>` for clips, an explicit "file missing" for `missing`), the state
  control and the internal note.
- **Board** — each card gains the unread badge when `open_reports` is a non-zero number.
  Absent when null.
- **Site detail** — a feedback panel: the switch, console capture, the widget key state,
  rotate, and the embed snippet to copy.
- ⚠ **These follow status's stack, which is not `home`'s**: inline styles and oklch custom
  properties in `src/theme/globals.css`, **no Tailwind and no shadcn/ui**, dark by default
  with a single `.light` class on `<html>`. Query keys `['reports', filters]`,
  `['report', ref]`, `['site', id, 'feedback-config']`; mutations invalidate the inbox and
  the board.
- ⚠ A fifth top-level nav item is not free — the 230 px side nav collapses to a drawer on
  mobile and holds four routes today. See §V3-10.

**Widget (Czech and English, its own everything).** Framework-free, closed shadow root,
own styles, own strings, no shared token file. It must render correctly inside `home`'s
light Tailwind pages **without inheriting or leaking a single rule**.

## §V3-8 Non-Functional Requirements

**Observability.** The sweep logs a run summary in the shape the poller and purge already
use: objects examined, marked missing, deleted, errors. A sweep that aborted logs *why* at
`error` and reports zero deletions rather than silence.

**Performance.** Attachment bytes never traverse the droplet in either direction —
uploaded by presigned PUT, read by presigned GET. The service's cost per report is one
insert, N `HEAD`s at claim, and N presign operations when the report is opened. The inbox
is a keyset query over an indexed column.

**Security and abuse.** The controls, in the order they actually bite:

1. **No accepted report, no upload URL** (V3-D07) — structural, and free.
2. **Exact-size signing** (V3-D08) — a huge file is refused at R2.
3. **Per-key and per-IP rate limits**, `429` with `Retry-After`.
4. **The kill switch** — one flag, one click, no key rotation.
5. **Honeypot and minimum dwell**, both made real by the signed ticket.
6. **Origin allow-list** — stops another *page* using a lifted key; stops nothing at a
   shell prompt, which is why it is fifth and not first.

**Privacy.** Screenshots and video can contain anything on the reporter's screen,
including other people's data. They are access-controlled to Karel's session, presigned
for five minutes, and deletable per report. **They are not encrypted, and this document
does not pretend otherwise.** The client IP is hashed with a deployment salt and never
stored. `page_url` can carry query-string secrets from a badly built host app, so the
dashboard does not make it clickable without an explicit action.

⚠ **The console tail is opt-in because of `home` v9, and the reason is specific.** `home`
built a privacy model in which a member's private notes and documents are unreadable **by
anyone, admins included** — the refusal is a 404, never a 403, so an id cannot even be
confirmed. A console tail shipped from a `home` page could carry a private note's title
into status, whose reader is Karel's admin session. That is not a hypothetical bypass of
v9's model; it is a side door with a different lock, and it would be opened by a feature
nobody connected to privacy at all. Hence: `console_capture` **off** by default, **on**
for `status` itself (no private-item model, one user, nothing to leak), **off** for `home`
and `fin` unless deliberately enabled (V3-D30, V3-D48).

**Backup — an accepted loss.** The attachments bucket is **not** backed up, mirrored or
versioned (V3-D of question 15). The durable record is the report text, which is in SQLite
and already replicated by Litestream to the `status/` prefix. Screenshots are context:
losing them loses convenience, not the record. This is a decision, not an oversight, and
the failure it accepts is that a wrong-prefix delete or a bad GC run is unrecoverable —
which is why V3-D06 and V3-D27 constrain the GC as tightly as they do.

**Credentials.** ⚠ **The application's R2 token reaches the attachments bucket and nothing
else** (V3-D21). This is `home`'s **D214** lesson applied before it costs anything: `home`
*declined* to let its app process hold `LITESTREAM_*` credentials, on the grounds that it
would widen the app's reach to the credentials for the household's entire database backup.
status is about to hold object-storage credentials for the first time. They must be the
narrow ones.

**Two v2 defects v3 repairs, first, in its own PR (V3-D40).**

1. ⚠ **Strip Prefix goes off in Coolify, and `StripAPIPrefix` stays defensively — with a
   derived list.** The live `401` on `/api/sites` beside a `404` on `/api/meta` is the
   signature of Strip Prefix being *enabled*, the opposite of what the repo docs instruct.
   Turning it off is the intended configuration; keeping the normalizer guards against the
   toggle being flipped back. But **the hand-written allow-list is exactly what failed**,
   and `TestStripAPIPrefix` enumerated the same four prefixes somebody remembered in
   August. So the re-prefix set is **derived from the routes the router actually
   registers**, and the test enumerates every top-level API prefix. `/meta` and `/reports`
   then fall out automatically, and so does whatever v4 adds. A defensive layer that needs
   manual maintenance is not defensive; it is a second place to be wrong.
2. ⚠ **CORS** — FR-25.

**Not repaired, recorded.** `/healthz` and `/readyz` remain unreachable at the public
origin (Traefik routes only `/api` to the backend), so status still cannot monitor itself
through its own URL. Coolify's container check on `:112` is unaffected. v3 does not fix
this; it stops being an unrecorded surprise.

## §V3-9 Configuration

All `STATUS_`-prefixed, loaded by the existing fail-fast loader that lists every problem
at once.

| var | default | notes |
|---|---|---|
| `STATUS_FEEDBACK_ENABLED` | `false` | master switch. When true, **every R2 var below is required at boot** |
| `STATUS_R2_ENDPOINT` | — | account endpoint |
| `STATUS_R2_BUCKET` | — | `ws-tilcer-status-feedback` |
| `STATUS_R2_ACCESS_KEY_ID` · `STATUS_R2_SECRET_ACCESS_KEY` | — | the narrow token of V3-D21 |
| `STATUS_FEEDBACK_MAX_FILES` | `3` | |
| `STATUS_FEEDBACK_MAX_IMAGE_MB` | `10` | |
| `STATUS_FEEDBACK_MAX_VIDEO_MB` | `50` | |
| `STATUS_FEEDBACK_MAX_TEXT_BYTES` | `8192` | the `413` boundary for the JSON body |
| `STATUS_FEEDBACK_RATE` | `0.0056` | reports/sec per widget key (≈20/hour), `rateDefault` idiom, as `STATUS_INGEST_RATE` |
| `STATUS_FEEDBACK_BURST` | `5` | |
| `STATUS_FEEDBACK_IP_RATE` | `0.0014` | ≈5/hour per client IP across all sites |
| `STATUS_FEEDBACK_IP_BURST` | `3` | |
| `STATUS_FEEDBACK_UPLOAD_TTL` | `10m` | presigned PUT lifetime |
| `STATUS_FEEDBACK_VIEW_TTL` | `5m` | presigned GET lifetime |
| `STATUS_FEEDBACK_UNCLAIMED_TTL` | `24h` | sweep threshold **and** the GC's minimum object age |
| `STATUS_FEEDBACK_MIN_DWELL_MS` | `3000` | |
| `STATUS_FEEDBACK_TICKET_SECRET` | — | required when enabled |
| `STATUS_IP_HASH_SALT` | — | required when enabled |

**Reused, not duplicated.** `STATUS_TRUSTED_PROXY_COUNT` (default `1` = Coolify's lone
Traefik) already governs client-IP resolution for logs and the login rate limit; the
feedback IP limiter uses **the same machinery** (V3-D23). A second, differently-behaved
XFF parser in one binary is how one of them ends up wrong.

**Cross-validation at boot** (V3-D24), alongside the existing
`ROLLUP_RETENTION_DAYS >= max(RETENTION_DAYS, UPTIME_WINDOW)` check:

- `FEEDBACK_UPLOAD_TTL < FEEDBACK_UNCLAIMED_TTL` — otherwise the sweep can delete an
  object whose presigned PUT has not yet expired, which is V3-D06's "GC that outruns an
  in-flight upload" arriving through the config file instead of the code.
- `FEEDBACK_MAX_VIDEO_MB >= FEEDBACK_MAX_IMAGE_MB` — a video cap below the image cap is
  always a typo.
- `FEEDBACK_MIN_DWELL_MS` under 30 000 — a dwell longer than half a minute is a disabled
  widget wearing a config value.

Fail fast, listing every problem at once, as the service already does.

**Not application configuration — bucket configuration** (V3-D22). The R2 bucket carries
its own CORS policy, and it is part of the deploy, not the code: **`PUT` allowed from the
origins in `STATUS_ALLOWED_ORIGINS`**, with `Content-Type` and `Content-Length` as allowed
headers. ⚠ **`*` is not acceptable on a bucket that accepts writes**, and a browser upload
will fail with no useful error if this is missed — the PUT is cross-origin to R2 even when
the report call was not. `docs/widget.md` records the policy so it is reproducible rather
than remembered.

**V3-D50 — CORS reuses `STATUS_ALLOWED_ORIGINS`, and the per-site allow-list is dropped.**

The brief left one modelling question open (V3-D44): where a per-site origin allow-list
lives, given that repairing v2's CORS bug means crash ingest needs one too, and crash has
no `feedback_site_config` row to read. **Reading the code dissolved the question.**
`STATUS_ALLOWED_ORIGINS` already exists — a CSV loaded by `config.csvDefault`, defaulting
to **`https://*.tilcer.cz`**, with wildcard matching already implemented in
`auth.originMatches`, feeding the CSRF middleware's origin check. One entry already covers
the entire fleet.

So: **CORS uses that existing service-level list**, no new configuration, no new column,
and crash ingest gets its repair with no data-model change at all. The per-site
`allowed_origins` column is **dropped from `feedback_site_config`**.

What that gives up is precision — a widget key lifted from `home` could be used from a
page on `fin`. What it buys is that the model does not grow a second origin allow-list
with different semantics from the one already enforcing CSRF. The trade is worth taking
because the precision was mostly illusory: an origin header is trivially forged outside a
browser, so a per-site list never stopped a determined actor at a shell prompt — only the
rate limits and the kill switch ever did. **Reverting is one column and one join** if a
site ever needs to be narrowed.

No configuration for privacy at all: `console_capture` is per-site data, not an env var.

## §V3-10 Open Questions

*Resolved during scoping (2026-09-02, `V3-feedback-brief.md`):* module `feedback` ·
separate `wk_` key · presigned PUT · hosted versioned widget · CZ+EN widget, EN admin ·
auth-only apps, `karel.tilcer.cz` deferred to v4 · reports never colour the board ·
images ≤10 MB and video ≤50 MB stored as-is · reports kept, attachments die with them ·
ref code, one-way · cross-site inbox, four states · presigned GET · v3 owns v2's debt ·
Strip Prefix off with the middleware kept · bucket `ws-tilcer-status-feedback` · `kind`
ships · integration is a separate PR per app.

*Resolved while writing this document, against the repo:* **V3-D50** (CORS reuses
`STATUS_ALLOWED_ORIGINS`; the per-site column is dropped) · **V3-D51** (feedback config
gets its own routes) · **V3-D52** (`feedback` mounts twice, mirroring `crash`) ·
**V3-D53** (`open_reports` is null, not zero, when the module is absent).

*Resolved before the handoffs (2026-09-02):* **V3-D54** (spike run against the real
bucket — **V3-D08 holds, by truncation**; FR-18 carries the measurements and FR-19 is
corrected accordingly) · **V3-D55** (the widget is neutral and self-contained, neither
status-branded nor host-themeable) · **V3-D56** (a built-in launcher *and*
`StatusFeedback.open()`) · **V3-D57**, below.

**Nothing now gates the design or engineering handoff.**

**V3-D57 — the debt ships first, on its own, and v3 lands in stages.**

- **PR 1 — the repairs.** Strip Prefix, CORS, `openapi.yaml` 0.3.0. Small, reviewable,
  independently deployable, and it makes the browser crash client work **for the first
  time** whatever subsequently happens to v3. Nothing in it depends on the feature.
- **PR 2 — the module.** Migrations, store, the eight routes, the sweep, the config.
- **PR 3 — the widget and the inbox.** The second Vite entry, the Nginx locations, the
  dashboard screens.

⚠ status has **no PRs at all** today — one commit built the entire service, and a second
pair fixed routing after deploy. That was survivable for a greenfield build with one
reader. v3 carries a routing change, a security repair, a contract rewrite, a new module
and a new distributable artifact; putting them in one unit means the repairs cannot ship
until the feature is ready, and that the as-built reconciliation has no seams to reason
about. Three PRs, in this order.

*Remaining, none blocking implementation:*

1. **Is `feedback_ticket` a table or an in-process map?** Specified as a table. One
   process, one writer connection, and a restart invalidating outstanding tickets is
   harmless — so a map is defensible and cheaper. The table's advantage is that the sweep
   and the rate limiter can both see it.
2. **Is the inbox a fifth nav item, or a tab on the board?** The 230 px side nav holds
   four routes and collapses to a drawer on mobile.
3. **Rate-limit numbers.** `≈20/hour` per key and `≈5/hour` per IP are guesses from a
   household's shape, not measurements. One env var each; revisit after a month.
4. **Does `latest.js` earn its existence?** With three embeds under Karel's own control,
   pinning `v1` everywhere may be simpler than maintaining a redirect that must fight an
   Nginx regex block for its cache headers.

## §V3-11 Acceptance Criteria

- [ ] Feedback is off for every site until deliberately enabled; a site with no config row
      behaves as disabled and the dashboard renders the switch without a special case.
- [ ] Enabling a site issues a `wk_` key shown **once**; the hash is stored and the
      plaintext is unrecoverable.
- [ ] Rotating the widget key invalidates the old one immediately and **a crash still
      ingests with the unchanged `ik_` key**.
- [ ] `GET …/feedback/config` returns `enabled:false` for a disabled site and the widget
      renders **no launcher at all** — not a button that fails when pressed.
- [ ] The guard chain returns, in order: `404` unknown site, `401` bad widget key, `403`
      disabled, `403` foreign origin, `429` over rate, `413` oversized body, `422` invalid,
      `202` — and the body is **not parsed** when the key is bad.
- [ ] A submission with the honeypot filled, a spent ticket, a ticket younger than
      `MIN_DWELL_MS`, or a content type outside the allow-list is a `422`.
- [ ] An accepted report returns one upload slot per declared file, and **there is no other
      route in the service that mints an upload URL**.
- [x] ✅ **The spike has been run and its answer is recorded in FR-18** (V3-D54, measured
      2026-09-02): R2 refuses a PUT whose declared length differs from the signed one
      (`403 SignatureDoesNotMatch`) and **truncates** one that lies about it, so no object
      can exceed the signed size. Content type is enforced the same way.
- [ ] A presigned PUT signed for N bytes stores **at most N**: an honestly-declared
      oversized body is a `403`, and a body that lies about its length yields an object of
      exactly N bytes — asserted against the real bucket, not a fake.
- [ ] The upload URL's `X-Amz-SignedHeaders` contains **`content-length`** — asserted in a
      test **before** any upload is attempted. Probe 4 showed that without it the bucket is
      an open upload endpoint that reports no error, so this is the assertion that stops a
      "simplification" or an SDK bump from silently removing the only size enforcement in
      the system.
- [ ] A signed `Content-Type` outside the allow-list is refused by R2, not merely unrecorded.
- [ ] Claim records the size R2 reports, not the declared size; a missing object becomes
      `missing` and **the report survives with its text**.
- [ ] An abandoned dialog leaves **zero bytes** in the bucket.
- [ ] Deleting a report removes the row and then its objects; a delete issued while R2 is
      unreachable leaves an orphan that the next sweep collects.
- [ ] Deleting a site cascades its reports and removes their objects **after** the
      transaction commits.
- [ ] **No R2 call is made inside a transaction** — asserted structurally, not by review.
- [ ] The sweep marks unclaimed attachments `missing` after the TTL, deletes only under the
      `feedback/` prefix, never deletes an object younger than the TTL, and **deletes
      nothing at all when the listing errors**.
- [ ] `RETENTION_DAYS` does not purge reports (`TestRetentionDoesNotPurgeFeedback`).
- [ ] The daily job runs rollup → purge → sweep, in that order.
- [ ] The board shows an unread badge from `open_reports` and the site's **colour is
      unchanged** by any number of reports; `ComputeColor` is untouched and a test says so.
- [ ] `open_reports` is `null`, not `0`, when the feedback module is not composed.
- [ ] The inbox pages by keyset cursor with the limit clamped 1..200, default 50.
- [ ] An attachment URL is presigned, expires within `VIEW_TTL`, and a `pending` or
      `missing` attachment yields `404` rather than a URL.
- [ ] **A cross-origin preflight to `/api/ingest/{siteId}` from an allow-listed origin
      succeeds and from a foreign origin does not**, and no response ever carries
      `Access-Control-Allow-Credentials`.
- [ ] **`clients/js/status-report.js` lands a crash from a page served by a different
      origin** — the v2 criterion that was false, now demonstrated.
- [ ] `GET /api/meta` returns 200 in production, and the `StripAPIPrefix` test enumerates
      **every** top-level API prefix the router registers rather than a hand-written list.
- [ ] `openapi.yaml` is **0.3.0**, validates as OpenAPI 3.1, describes every built route
      including the four v2 omissions, and documents `sessionCookie` + `csrfToken` rather
      than `bearerAuth`.
- [ ] `/widget/v1.js` is served with a one-year immutable cache; `/widget/latest.js` 302s
      with `max-age=300` from an **exact** Nginx location; an unknown `/widget/*.js` is a
      404 and never the SPA shell.
- [ ] The widget renders correctly inside a light Tailwind page, inherits no host styles
      and leaks none, and **never throws into the host app** under any failure.
- [ ] `StatusFeedback.open()` opens the dialog, and `data-launcher="none"` suppresses the
      floating button without suppressing the API.
- [ ] The dialog is fully keyboard-operable: focus trap, `Escape` closes and returns focus.
- [ ] The widget speaks Czech by default and English on `data-lang="en"`; the admin UI is
      English only.
- [ ] A site with `console_capture` off sends no console lines and the dialog does not
      offer them; with it on, the dialog **shows the reporter what will be sent**.
- [ ] The client IP is never written to a row; only `ip_hash` is.
- [ ] Boot fails, listing every problem, when `FEEDBACK_ENABLED` is true and any R2 var is
      absent, or when `UPLOAD_TTL >= UNCLAIMED_TTL`.
- [ ] The bucket's own CORS policy allows `PUT` from the allow-listed origins and **not**
      from `*`, and is written down in `docs/widget.md` rather than remembered.
- [ ] `docs/widget.md` exists and a monitored app can be integrated from it alone —
      **including the three CSP directives** (V3-D58), the bucket's CORS policy, and the
      key-rotation flow.
- [ ] ⚠ **A real report is filed by hand, from a real second origin, before v3 is called
      done** — key, CORS preflight, presign, PUT, claim, inbox, presigned view, delete.
      v3 ships no integration (V3-D47), so nothing else in this list proves the path works
      end to end. `home` v9's `StorageBlobs` bug compiled, passed every test, and reported
      0 B with an empty listing: *"it was found by opening the page."*
