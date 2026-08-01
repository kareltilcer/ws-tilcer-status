# Engineering Handoff — Status (Monitoring & Crash Reporting)

> For: **Claude Code** · Owner: Karel · Last updated: 2026-08-01
> **Source of truth:** `PRD.md` (behaviour, data model, acceptance criteria) and `openapi.yaml` (the API contract — implement it exactly). `../../CLAUDE.md` conventions are inherited and authoritative. This doc is the build plan, not a re-spec.

## 1. Scope

Build the `status` service: a Go **modular monolith** backend (two modules — `monitoring`, `crash` — over a shared `sites` registry) plus a React SPA dashboard, deployed as a two-app pair on `status.tilcer.cz`. Sites are added in the dashboard with a **user-supplied id**; that same id is the target of crash reports.

## 2. Non-negotiable conventions (from CLAUDE.md + REGISTRY)

- **Deploy:** two Coolify apps, one origin (mirror `home`/`fin`). Backend = API-only Go image mapped to `status.tilcer.cz/api`; frontend = static Nginx SPA on the catch-all domain. Traefik path-routes. **Do not enable Strip Prefix** on the backend — routes are served under `/api` or they 404.
- **Ports:** backend **112**, frontend **155**.
- **Backend stack:** Go **1.26**, `chi` router, `modernc.org/sqlite` (embedded), **Goose** migrations.
- **Backups:** **Litestream → R2**, prefix **`status/`**. Fresh builds restore initial load from R2.
- **Frontend stack:** React + TypeScript + **Vite**, **TanStack Query** (`useQuery`), static Nginx image.
- **Auth:** shared `auth` backend, site id `status`, **Mode B** (self-hosted login + own session cookie), mirroring `home`. JWT 15-min for API + session cookie for long-term.
- **Secrets/config:** Coolify env vars only. No secrets in the repo.
- **Observability baseline:** `GET /healthz`, `GET /readyz` (with SQLite check), structured JSON logs to stdout, per-request logging (method, path, status, latency).
- **Repo:** `ws-tilcer-status`.

## 3. Suggested layout

```
cmd/server/            main: config, wiring, graceful shutdown
internal/
  httpx/               chi middleware: request log, auth (JWT+session), rate limit, body-cap, recover
  auth/                Mode B: self-hosted login, own session, auth-backend introspection + cache
  store/               sqlite open, PRAGMA foreign_keys=ON, migrations runner (Goose)
  sites/               site registry: CRUD, ingest-key gen/hash/rotate, color computation
  monitoring/          poller (ticker), check_result writes, fail_streak, /readyz targeting
  crash/               public ingest, fingerprint, grouping, groups/events browse + triage
  retention/           daily purge job
migrations/            Goose SQL
clients/go/            copy-in Go helper (FR-12)
clients/js/            browser JS snippet (FR-12)
docs/                  detailed integration docs (FR-12)
README.md              quick-start (FR-12)
openapi.yaml           the contract (copy of this spec, kept in sync)
web/                   React SPA
```

## 4. Build order (milestones)

- **M0 — Scaffolding.** Repo, Go module, chi, sqlite+Goose, Litestream config, health probes, structured logging + request-log middleware, Coolify two-app deploy skeleton on ports 112/155. Mode B auth wired (login, session, introspection cache).
- **M1 — Sites registry.** Migrations for all five tables (`site`, `check_result`, `check_rollup`, `crash_group`, `crash_event`). `sites` CRUD per `openapi.yaml`; ingest-key generate → hash-store → return-once; rotate-key. Color computation as a pure function (default `unknown`).
- **M2 — Crash module.** Public `POST /api/ingest/{siteId}` with `X-Ingest-Key` auth, body-cap (413) before parse, per-key rate limit (429), fingerprint + group upsert + event insert, cached-color update. Admin browse (`/sites/{id}/crashes`, `/crashes/{groupId}`) + triage (`PATCH`).
- **M3 — Monitoring module.** Poller goroutine (ticker `CHECK_INTERVAL`, 10s timeout, bounded concurrency), `check_result` writes, `fail_streak` maintenance, color recompute with the **2-fail debounce**. Plus the **uptime aggregate (FR-13)**: nightly rollup job → `check_rollup`, cached `site.uptime_pct`, and `GET /api/sites/{id}/uptime` (rollup-backed) with `window`/`buckets`.
- **M4 — Retention.** Daily sweep **after** the rollup job: purge `check_result`/`crash_event` older than `RETENTION_DAYS`, `check_rollup` older than `ROLLUP_RETENTION_DAYS`; empty groups removed.
- **M5 — Frontend.** Board, site detail, add/edit, crash group, login — per `HANDOFF-design.md` once designs land. TanStack Query keys/invalidation per PRD §7.
- **M6 — Docs deliverable (FR-12).** `README.md` quick-start, `docs/` integration reference, `clients/go` + `clients/js` helpers. **Treat as shippable scope, not an afterthought.**

## 5. Implementation notes & gotchas

- **Ingest keys:** format `ik_` + 32 random url-safe bytes. Store only a hash (SHA-256 is fine — the key is high-entropy, so a fast hash + constant-time compare is acceptable; do **not** log plaintext). Return plaintext exactly once on create/rotate. Rotation invalidates the old key immediately.
- **Fingerprint (default):** `hash(site_id + level + normalize(message) + first_stack_frame)`. Normalize the message before hashing: strip/replace digits, hex, UUIDs, memory addresses, and quoted paths so "user 41 not found" and "user 99 not found" group together. Honor a client-supplied `fingerprint` verbatim when present. Put the algorithm behind one function with unit tests — it defines grouping quality.
- **Grouping:** upsert `crash_group` by unique `(site_id, fingerprint)`: create with `first_seen`, else bump `last_seen` + `count`. If the group is `resolved` and `REOPEN_ON_REGRESSION` is true, reopen it. `title` from the first event's message; `level` = highest seen.
- **Rate limit + size cap:** per **ingest key** token bucket (`INGEST_RATE`, default 60/min, burst 120) → 429 with `Retry-After`. Enforce `MAX_INGEST_BYTES` (64 KB) via a limited reader **before** JSON decode → 413.
- **Color computation (pure, cached):** recompute + persist `site.cached_color` after every check, every ingest, and every triage. Rules exactly per PRD FR-6 — **red only when `fail_streak >= RED_FAIL_THRESHOLD` (default 2)**; a single failure holds the prior color. Precedence red > orange > green > unknown. Orange counts only **open** groups within `crash_window_hours`.
- **Poller:** one ticker; iterate monitor-enabled sites with bounded concurrency; `http.Client` with `CHECK_TIMEOUT` (10s), follow redirects; `ok` = status matches `expected_status` (or any 2xx by default); record latency; increment/reset `fail_streak`; a failing site never blocks others. Recommend `/readyz` as the `monitor_url` for our own services.
- **Uptime rollups (FR-13):** never scan raw `check_result` for the board or the uptime endpoint. A nightly job aggregates each site's day into `check_rollup` (`ok_count`, `fail_count`, `latency_p50/p95`) and refreshes cached `site.uptime_pct` over `UPTIME_WINDOW`. **Order matters:** rollup must run before the retention purge, or the last day's raw checks are deleted before they're aggregated. `GET /api/sites/{id}/uptime` composes `buckets[]` from rollups; a bucket with no checks → `ok_pct: null` (gap), not `0`. `uptime_pct` is `null` for monitoring-disabled / never-checked sites → the board renders "monitoring off," and such sites are never red.
- **DB:** `PRAGMA foreign_keys=ON`; FK `ON DELETE CASCADE` so deleting a site removes its checks/rollups/groups/events. All timestamps RFC3339 **UTC**.
- **Pagination:** opaque cursor (e.g. base64 of `last_seen`+`id`) on `checks` and `crashes` lists; `limit` 1–200, default 50.
- **Auth:** admin endpoints require `bearerAuth` (JWT) with `sessionCookie` fallback; `POST /api/ingest/{siteId}` uses `ingestKey` only; health probes are open. Mirror `home`'s Mode B session handling and introspection cache.
- **Errors:** uniform `{ "error": string, "detail"?: string }` (the `Error` schema) across all failures.

## 6. Config (env — values in Coolify)

`AUTH_BASE_URL`, `AUTH_SITE_ID=status`, `SESSION_SECRET`, `CHECK_INTERVAL=5m`, `CHECK_TIMEOUT=10s`, `RED_FAIL_THRESHOLD=2`, `UPTIME_WINDOW=90d`, `RETENTION_DAYS=90`, `ROLLUP_RETENTION_DAYS=400`, `MAX_INGEST_BYTES=65536`, `INGEST_RATE=60/min` (burst 120), `REOPEN_ON_REGRESSION=true`, `LITESTREAM_*` + R2 creds (prefix `status/`), `PORT=112` (frontend app `155`). No BE→BE shared secret in v1. Frontend UI is **English only**.

## 7. Testing & acceptance

Map tests to **PRD §11 acceptance criteria**. Minimum:

- **Unit:** fingerprint normalization/grouping; color computation incl. the 2-fail debounce and recovery; rate-limit bucket; body-cap enforcement; key hash + constant-time compare.
- **Integration:** ingest happy path (202 + group upsert), wrong key (401), unknown site (404), oversized (413), over-rate (429); site CRUD incl. duplicate id (409) and validation (422); cascade delete; retention purge boundary.
- **Uptime aggregate:** rollup produces correct `uptime_pct` + p50/p95; `/uptime` buckets partition the window with `ok_pct: null` for empty buckets; rollup-runs-before-purge ordering (90-day uptime survives a purge); `uptime_pct` null + "monitoring off" for a monitoring-disabled site, which never goes red.
- **Contract:** responses conform to `openapi.yaml` (consider generating types/validating against it).
- **Client helpers:** verify the Go helper and JS snippet each land a crash end-to-end and **fail safe** — never crash or block the host app when ingest errors.

## 8. Definition of done

All PRD §11 acceptance criteria pass; deployed as two Coolify apps (BE 112 / FE 155) on `status.tilcer.cz` with `/api` routing and no strip-prefix; Litestream replicating to R2 `status/` with a verified fresh-build restore; `/healthz` + `/readyz` behaving per baseline; the FR-12 docs + quick-start + Go/JS helpers shipped and verified; `openapi.yaml` matches the built surface; REGISTRY row moved from "spec draft" to implemented.

## 9. Deferred decisions (defaults set — safe to proceed)

Orange window 24h; single-event ingest (no batch in v1); frontend ingest keys public by design (Sentry-DSN model). See PRD §10 — none block implementation.
