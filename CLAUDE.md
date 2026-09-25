# CLAUDE.md — ws-tilcer-status

Conventions for working in this repo. It follows the `home`/`fin` fleet pattern; when in doubt, mirror
`../ws-tilcer-home`.

## What this is

`status.tilcer.cz` — fleet monitoring + crash reporting. Two Coolify apps, one origin:
- `backend/` — Go modular monolith, API-only, port **112**, served at `status.tilcer.cz/api`.
- `frontend/` — React + Vite SPA, port **80**, served at `status.tilcer.cz` (catch-all). It also
  builds and serves the **feedback widget** (`/widget/v1.js`) from a second Vite config.

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
  `db/txprobe` is the ONE "no network call while the connection is held" detector, shared by the
  blob and mail fakes — never copy it into a third.
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
- `internal/notify` — email notifications, as an **outbox** (`migrations/30001_notify.sql`). crash
  ingest, feedback submit and the poller each declare a small `Notifier` interface and call it as the
  **last** statement of their own transaction; `notify.Notifier` decides and queues there with no
  network, and `notify.Worker` (a scheduler tick) folds the queue into one digest per window, stores
  it rendered, and sends it after the commit, retrying under the same idempotency key. Crash
  "armed/announced" and site up/down state are notify's own tables — ⚠ not `cached_color`, which a
  URL edit resets and a monitoring switch-off leaves red. `mail/` is the provider behind an interface
  (stdlib Resend client + a log-only dev mailer); `mail/mailtest` is the fake.
- `internal/retention` — daily purge (runs **after** the rollup). ⚠ It does **not** touch feedback:
  a report is a hand-written artifact and is kept until deleted (`TestRetentionDoesNotPurgeFeedback`).
- `internal/scheduler` — the poller ticker, the notify worker ticker (only when a mail provider
  exists) + the daily **rollup → purge → notification prune → feedback sweep** timer (net-new; `home`
  has none). The sweep runs last because it is the only step that talks to the network.
- `internal/bootstrap` — assembles the migration sequence (platform sessions + sites schema +
  feedback tables + notify tables).
- `cmd/status/main.go` — config → open → migrate → auth wiring → mailer → scheduler → serve →
  graceful shutdown. `daily.go` holds the daily chain (rollup → purge → prune → sweep) as a named
  function, because its order is normative and a closure cannot be tested. ⚠ `defer jobsWG.Wait()` is
  registered **before** `defer cancelJobs()`, so every return path cancels, then joins the jobs, and
  only then drains deletes and closes the DB.

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
  `NewRouter`), because chi runs group middleware only on a matched route. `Retry-After` is
  **exposed** (`Access-Control-Expose-Headers`): only seven response headers are CORS-safelisted and
  that is not one of them, so without it the widget's 429 countdown silently becomes its fallback.
- Color is **computed on read** in list/detail (orange ages out by time); `cached_color` is a
  write-through fallback updated after every check, ingest, and triage.
- The **dashboard** is English only (unlike the Czech `home`/`fin` UIs). The **widget** is the one
  translated surface: Czech by default, English on `data-lang="en"`, both string sets in the bundle.

### Migrations
Numeric filename prefix orders them globally: platform sessions `02xxx`, sites schema `10xxx`,
feedback `20xxx`, notify `30xxx`. Every child table FKs to `site` with `ON DELETE CASCADE`; `foreign_keys` is a DSN
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

### Email (notify)
⚠ **No mail send may run inside a transaction or with a `rows` cursor open** — the R2 rule, for the
same reason, asserted the same way (`TestNoMailSendInsideATransaction` + its probe-proof partner).
⚠ **A notifier hook is the LAST statement of the producer's transaction, and runs inside a
`SAVEPOINT`.** Any failure or panic of its own is rolled back to the savepoint and logged — it must
never cost the crash, report or check it describes. Its error reaches the producer only when SQLite
has already rolled the whole transaction back (then the commit would fail anyway), and the producer
must return it rather than run another statement, which would commit on its own in autocommit.
⚠ The outbox has **no UNIQUE constraint**: it is written inside feedback's insert transaction, whose
retry loop reads any "UNIQUE constraint failed" as a ref collision. A digest is rendered **once** and
stored — a retry must send the byte-identical request, or Resend refuses the reused idempotency key
(409); retries stop at 23 h, inside Resend's 24 h window. ⚠ **No digest is assembled while another is
pending** — the hourly cap counts digests created, so without that a provider outage mints one per
window and the recovery sends the backlog in a minute. ⚠ Because of that hold, a pending digest whose
**envelope** is stale (`STATUS_MAIL_FROM` or the saved recipients changed) is superseded — failed, its
events put back in the outbox — or an envelope the provider refuses would hold every notification
for 23 h. Payloads are excerpts only: never a stack, never a reporter's label, page, browser, console
or IP hash.

## Frontend (`frontend/`)

Two artifacts from one build, sharing nothing but the repository:

- **The dashboard** — React 19 + Vite + TanStack Query, inline styles over oklch custom properties in
  `src/theme/globals.css`, dark by default via a single `.light` class. **No Tailwind, no shadcn/ui.**
- **The widget** (`src/widget/*`, built by `vite.widget.config.ts` into `dist/widget/v1.js`) —
  vanilla DOM in a **closed shadow root**, no framework, its own sRGB token set (V3-D55: deliberately
  not derived from status's oklch tokens, and not host-themeable). It renders **nothing at all**
  until `GET …/feedback/config` answers `enabled: true`, and it never throws into the host app.
  ⚠ The build is **ASCII-only** (`asciiOnly` plugin): a cross-origin classic script does not inherit
  the host document's UTF-8, so without that — and without `charset utf-8` in `nginx.widget.conf` —
  every Czech string in it becomes mojibake in what it shows *and* in what it sends. The plugin's
  post-condition reads the **written file** back off disk; re-testing the regex on the string the
  replace just produced cannot fail and proves nothing.
  ⚠ Uploads PUT straight to R2, so an oversized file must be refused **client-side**: the URL is
  signed for the size the widget declared, and `Content-Length` is a header the browser will not let
  script set.
  ⚠ "Never throws into the host" is enforced in **one place**, `dom.ts`'s `el()`, which wraps every
  listener it binds and catches a promise a handler returns. Per-call-site `try` is how the
  launcher — the most-clicked element v3 ships — ended up the one unguarded entry point.
  ⚠ `frontend/nginx.widget.conf` holds the widget's charset and cache contract as **one** file,
  `include`d at server level by both `nginx.conf` and `nginx.harness.conf` (baked to
  `/etc/nginx/widget.conf`, **not** under `conf.d/`, which nginx auto-includes at http level where
  `location` will not parse). FR-24 fixes those headers and §V3-11 checks for them; two copies means
  a harness that proves a policy production does not serve.

## Testing
`cd backend && go test ./...`. `internal/apitest` drives the real router over HTTP (dev-bypass auth,
temp DB) and covers the PRD §11 acceptance criteria end to end. `cd frontend && npm test` runs the
widget's vitest suite (jsdom) — the dashboard has none. ⚠ CORS, a host's CSP and the bundle's charset
fail only cross-origin: verifying the widget means serving it to a page on another origin
(`docs/widget.md` §11).

## Auth (Mode B)
`status` hosts its own login and owns its session (random token, SHA-256-hashed in `sessions`); the
browser carries no bearer token. It calls auth BE→BE (`X-Service-Secret`) at `/internal/login`
(+`/internal/login/mfa`) and re-mints roles via `/internal/token/mint` every ~15 min. MFA is not handled
in-app (link out to auth); Google OAuth stays auth-hosted. Requires a `status` site + a service client
bound to it in `auth.tilcer.cz` (see README → Deploy).

## Deploy & backup
Two Coolify apps (BE 112 `/api`, FE 80 catch-all). Litestream → R2 prefix `status/`; fresh build
restores from R2 via `docker-entrypoint.sh`. Secrets via Coolify env only.
