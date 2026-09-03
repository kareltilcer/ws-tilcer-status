# CLAUDE.md — ws-tilcer-status

Conventions for working in this repo. It follows the `home`/`fin` fleet pattern; when in doubt, mirror
`../ws-tilcer-home`.

## What this is

`status.tilcer.cz` — fleet monitoring + crash reporting. Two Coolify apps, one origin:
- `backend/` — Go modular monolith, API-only, port **112**, served at `status.tilcer.cz/api`.
- `frontend/` — React + Vite SPA, port **80**, served at `status.tilcer.cz` (catch-all).

⚠ **Strip Prefix must be OFF** on the backend — routes are under `/api`. `httpx.StripAPIPrefix` is a
defensive fallback for the toggle being flipped back, and its re-prefix set is **derived** from the
routes the router registers (`httpx.APISegments`) — never hand-written. The hand-written list it
replaced omitted `/meta`, which is why `GET /api/meta` 404'd in production.

## Backend layout (`backend/`)

Compile-time modular monolith:
- `internal/platform/*` — shared infra: `config` (env, `STATUS_` prefix), `db` (sqlite open + goose
  migrations + `WithTx`), `httpx` (chi router, `Error` envelope `{error, detail}`, health probes, role
  gates), `auth` (Mode B: self-hosted login + own session + CSRF), `reqctx`, `idgen`, `paging`
  (cursor helpers), `timeutil` (the one fixed-width RFC3339 UTC layout — all stored timestamps use it).
- `internal/sites` — the shared registry. Owns the **v2** schema (one migration,
  `migrations/10001_init.sql`, all five tables). CRUD, ingest-key lifecycle, and the pure `ComputeColor`
  + `RecomputeAndPersist` (FR-6). It must never import a feature module: the board's `open_reports`
  badge and the object half of the site cascade are **interfaces it declares** (`ReportCounter`,
  `ObjectPurger`) and `cmd/status` injects — never a package-level global.
- `internal/monitoring` — poller (2-fail red debounce), nightly rollup, rollup-backed `/uptime`.
- `internal/crash` — public key-authenticated ingest (guard chain 404→401→429→413→422→202),
  fingerprint grouping, admin browse/triage.
- `internal/feedback` — user bug reports with R2 attachments (v3). The first module to own a
  migration block (`migrations/20001_feedback.sql`, four tables); mounts **twice** — the gated inbox
  and per-site config through `RegisterRoutes`, the three public widget routes through
  `MountPublicAPI`, as `crash` does. `blob/` wraps R2 behind an interface; `blob/blobtest` is the fake,
  which **truncates** an oversized upload because that is what R2 does.
- `internal/retention` — daily purge (runs **after** the rollup). ⚠ It does **not** touch feedback:
  a report is a hand-written artifact and is kept until deleted (`TestRetentionDoesNotPurgeFeedback`).
- `internal/scheduler` — the poller ticker + the daily **rollup → purge → feedback sweep** timer
  (net-new; `home` has none). The sweep runs last because it is the only step that talks to the
  network.
- `internal/bootstrap` — assembles the migration sequence (platform sessions + sites schema).
- `cmd/status/main.go` — config → open → migrate → auth wiring → scheduler → serve → graceful shutdown.

### Conventions
- Go 1.26, `chi` v5, `modernc.org/sqlite` (CGO off, `SetMaxOpenConns(1)`, WAL), Goose migrations, slog JSON.
- **Single writer connection.** Never run a query while an outer `rows` cursor is open (it deadlocks on
  the one connection) — drain into a slice and `Close()` first. Same for a `WithTx`: use the `tx` for
  every read/write inside it.
- All timestamps are `timeutil.Layout` (fixed-width, UTC) so **string comparison is a valid time order**
  — cursors and windowed queries rely on it. Never store `time.RFC3339Nano` directly (variable width).
- Errors: return `*httpx.APIError` (or the `httpx.Err*` constructors); `httpx.WriteError` renders the
  `{error, detail}` envelope.
- **CORS is on the public group only** (`MountAuth` + `MountPublicAPI`) and reuses
  `STATUS_ALLOWED_ORIGINS` — there is exactly one origin allow-list (V3-D50). The gated group stays
  same-origin. ⚠ `Access-Control-Allow-Credentials` is **never** sent: those endpoints authenticate by
  key, not by cookie. Every public path also gets an explicit `OPTIONS` handler (derived in
  `NewRouter`), because chi runs group middleware only on a matched route.
- Color is **computed on read** in list/detail (orange ages out by time); `cached_color` is a
  write-through fallback updated after every check, ingest, and triage.
- UI language is **English only** (unlike the Czech `home`/`fin` UIs).

### Migrations
Numeric filename prefix orders them globally: platform sessions `02xxx`, sites schema `10xxx`,
feedback `20xxx`. Every child table FKs to `site` with `ON DELETE CASCADE`; `foreign_keys` is a DSN
pragma (per-connection).

### Object storage (feedback)
⚠ **No R2 call may run inside a transaction or with an outer `rows` cursor open.** One connection
means a network round-trip holds the service's only writer for the length of someone else's TCP
timeout. Collect keys, close, commit, *then* talk to R2 — asserted structurally by
`TestNoObjectStorageCallInsideATransaction`, whose probe is itself proven to fail by the test beside
it. Deletion order is normative: keys are read **inside** the transaction, objects deleted **after**
it commits. The presigned PUT signs `Content-Type` **and** `Content-Length`; dropping the latter turns
the bucket into an open upload endpoint that reports no error, which is what
`TestPresignPutSignsContentLength` exists to prevent.

## Testing
`cd backend && go test ./...`. `internal/apitest` drives the real router over HTTP (dev-bypass auth,
temp DB) and covers the PRD §11 acceptance criteria end to end.

## Auth (Mode B)
`status` hosts its own login and owns its session (random token, SHA-256-hashed in `sessions`); the
browser carries no bearer token. It calls auth BE→BE (`X-Service-Secret`) at `/internal/login`
(+`/internal/login/mfa`) and re-mints roles via `/internal/token/mint` every ~15 min. MFA is not handled
in-app (link out to auth); Google OAuth stays auth-hosted. Requires a `status` site + a service client
bound to it in `auth.tilcer.cz` (see README → Deploy).

## Deploy & backup
Two Coolify apps (BE 112 `/api`, FE 80 catch-all). Litestream → R2 prefix `status/`; fresh build
restores from R2 via `docker-entrypoint.sh`. Secrets via Coolify env only.
