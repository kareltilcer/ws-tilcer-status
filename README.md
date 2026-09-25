# status — fleet monitoring & crash reporting

`status.tilcer.cz` is an internal ops dashboard that shows, at a glance, whether every service on
the droplet is healthy (**🟢 green / 🟠 orange / 🔴 red / ⚪ unknown**), combining active uptime
polling with crash reports the other services send in.

- **Backend** — a Go modular monolith (`backend/`): `monitoring` (uptime poller + rollups), `crash`
  (public ingest + grouping) and `feedback` (user reports + R2 attachments), over a shared `sites`
  registry. Embedded SQLite, Litestream → R2 backup. Serves `status.tilcer.cz/api`.
- **Frontend** — a React + Vite SPA (`frontend/`) behind Mode B auth, serving `status.tilcer.cz`,
  plus the framework-free **feedback widget** at `/widget/v1.js` that the monitored apps embed.
- **Clients** — copy-in [`clients/go`](clients/go) and [`clients/js`](clients/js) crash reporters,
  and [`docs/widget.md`](docs/widget.md) for the feedback widget.

The full API contract is [`backend/openapi.yaml`](backend/openapi.yaml); behaviour and the data
model are in [`handoff/v2/PRD.md`](handoff/v2/PRD.md).

---

## Quick start — your first crash on the board (a few minutes)

1. **Add a site.** In the dashboard, click **Add site**, choose an `id` you'll use everywhere (e.g.
   `yarnlog`), and save. For a service you also want *monitored*, set its **Monitor URL** to that
   service's `/readyz` (see [Monitoring your own services](#monitoring-your-own-services)).

2. **Copy the ingest key** — it is shown **once** in the create dialog (you can rotate it later, but
   you cannot see this one again). Copy the ingest URL and the `X-Ingest-Key`.

3. **Send a test crash** with a raw `curl` (replace the id and key):

   ```sh
   curl -sS -X POST https://status.tilcer.cz/api/ingest/yarnlog \
     -H "X-Ingest-Key: ik_your_key_here" \
     -H "Content-Type: application/json" \
     -d '{"message":"hello from curl","level":"error","environment":"prod"}'
   # → 202 {"group_id":1,"event_id":1}
   ```

4. **…or drop in a client helper** instead of raw curl:
   - **Go:** copy [`clients/go/statusreport`](clients/go/statusreport) and see its usage below.
   - **Browser:** include [`clients/js/status-report.js`](clients/js/status-report.js).

5. **See it on the board.** The site flips to **🟠 orange** (reachable but noisy) and the crash
   appears under the site's Crashes list, grouped by fingerprint.

### Go helper

```go
import "github.com/kareltilcer/ws-tilcer-status/clients/go/statusreport"

// STATUS_INGEST_URL=https://status.tilcer.cz/api/ingest/yarnlog
// STATUS_INGEST_KEY=ik_...
sr, _ := statusreport.NewFromEnv(statusreport.WithRelease("yarnlog@2026.31.2"))

func main() {
    defer sr.Recover() // report a panic as fatal, then re-panic

    if err := doWork(); err != nil {
        sr.Report(err, statusreport.WithContext(map[string]any{"route": "/api/summary"}))
    }
}
```

### Browser snippet

```html
<script src="/status-report.js"></script>
<script>
  StatusReport.init({
    url: "https://status.tilcer.cz/api/ingest/yarnlog",
    key: "ik_your_public_browser_key", // public by design, like a Sentry DSN
    environment: "prod",
    release: "yarnlog@2026.31.2",
  });
</script>
```

Both helpers are **fire-and-forget** and **fail safe** — an ingest failure never blocks or crashes
the host app. Full payload reference: [`docs/integration.md`](docs/integration.md).

---

## The status system

| State | Meaning | When |
|---|---|---|
| 🟢 **green** | Healthy | Reachable and no crashes in the site's recent window (default 24h) |
| 🟠 **orange** | Noisy | Reachable but ≥1 recent crash in an open group |
| 🔴 **red** | Down | Failed health checks — **debounced**: only after 2 consecutive failures (~10 min at 5-min polling) |
| ⚪ **unknown** | No data yet | Monitored but not yet checked, and no crashes |

Precedence **red > orange > green > unknown**. A **monitoring-off** (crash-only) site is never red;
its status reflects crashes only, and it shows a "monitoring off" affordance instead of an uptime %.

### Monitoring your own services

Point a site's **Monitor URL** at the service's **`/readyz`** (not `/healthz`): `/readyz` includes the
SQLite connectivity check, so "green" means actually able to serve. The poller GETs it every
`STATUS_CHECK_INTERVAL` (default 5m) with a `STATUS_CHECK_TIMEOUT` (default 10s), following redirects.
A check is OK when the status matches `expected_status`, or is any 2xx when `expected_status` is the
default 200. (To require an *exact* status, set `expected_status` to a non-200 value.)

---

## Local development

```sh
# Backend (offline dev bypass — no auth service needed):
cd backend
STATUS_DEV_AUTH_BYPASS=true STATUS_DB_PATH=./dev.db STATUS_ADDR=:112 go run ./cmd/status

# Frontend (proxies /api to :112):
cd frontend
npm install && npm run dev

# Or the whole two-app image locally:
docker compose up --build   # → http://localhost:1155
```

Run the tests:

```sh
(cd backend && go test ./...)    # the Go suite
(cd frontend && npm test)        # the widget's vitest suite
```

`npm run build` emits both artifacts: the hashed SPA into `frontend/dist/`, and the widget into
`frontend/dist/widget/v1.js`. ⚠ Testing the widget properly means serving it to a page on **another
origin** — CORS, the host's CSP and the bundle's charset are all cross-origin-only failures, and a
same-origin test proves none of them. See [`docs/widget.md`](docs/widget.md) §11.

---

## Configuration (Coolify env vars)

Secrets live in Coolify only — never in the repo.

| Var | Default | Purpose |
|---|---|---|
| `STATUS_ADDR` | `:112` | HTTP listen address (backend port **112**) |
| `STATUS_DB_PATH` | *(required)* | SQLite file path (persisted volume) |
| `STATUS_SITE_KEY` | `status` | auth site key |
| `AUTH_BASE_URL` | *(required)* | shared auth service base URL |
| `STATUS_AUTH_SERVICE_SECRET` | *(required)* | BE→BE `X-Service-Secret` (auth service client) |
| `STATUS_AUTH_JWT_SECRET` | *(required)* | shared HS256 secret to verify auth's tokens |
| `STATUS_AUTH_JWT_ISSUER` | *(any)* | optional expected token `iss` |
| `STATUS_ALLOWED_ORIGINS` | `https://*.tilcer.cz` | Origin allowlist — CSRF gate **and** CORS on the public endpoints |
| `STATUS_SESSION_TTL_DAYS` | `90` | session sliding window |
| `STATUS_ROLE_REFRESH_MINUTES` | `15` | role re-mint interval |
| `STATUS_CHECK_INTERVAL` | `5m` | poll interval |
| `STATUS_CHECK_TIMEOUT` | `10s` | per-check HTTP timeout |
| `STATUS_POLL_CONCURRENCY` | `8` | bounded concurrent checks |
| `STATUS_RED_FAIL_THRESHOLD` | `2` | consecutive failures before red |
| `STATUS_UPTIME_WINDOW` | `90d` | rolling window for cached uptime_pct |
| `STATUS_RETENTION_DAYS` | `90` | raw check/crash purge window |
| `STATUS_ROLLUP_RETENTION_DAYS` | `400` | rollup retention (kept longer than raw) |
| `STATUS_MAX_INGEST_BYTES` | `65536` | ingest body cap → 413 |
| `STATUS_INGEST_RATE` | `60/min` | per-site ingest rate |
| `STATUS_INGEST_BURST` | `120` | per-site token-bucket burst |
| `STATUS_REOPEN_ON_REGRESSION` | `true` | reopen resolved groups on new events |
| `STATUS_DAILY_JOB_AT` | `00:15` | daily rollup → purge → notification prune → feedback sweep time (UTC) |
| `STATUS_FEEDBACK_ENABLED` | `false` | master switch for the feedback module. **When true, every `STATUS_R2_*` var below plus the ticket secret and IP hash salt is required at boot** |
| `STATUS_R2_ENDPOINT` | — | `https://<account>.r2.cloudflarestorage.com` |
| `STATUS_R2_BUCKET` | — | `ws-tilcer-status-feedback` |
| `STATUS_R2_ACCESS_KEY_ID` / `STATUS_R2_SECRET_ACCESS_KEY` | — | ⚠ a token scoped to the **attachments bucket alone** — it must not reach the Litestream bucket |
| `STATUS_FEEDBACK_TICKET_SECRET` | — | HMAC secret for single-use submission tickets |
| `STATUS_IP_HASH_SALT` | — | salt for the reporter IP digest (**the IP itself is never stored**) |
| `STATUS_FEEDBACK_MAX_FILES` | `3` | attachments per report |
| `STATUS_FEEDBACK_MAX_IMAGE_MB` / `STATUS_FEEDBACK_MAX_VIDEO_MB` | `10` / `50` | per-file caps, signed into the upload URL |
| `STATUS_FEEDBACK_MAX_TEXT_BYTES` | `8192` | report body cap → 413 |
| `STATUS_FEEDBACK_RATE` / `STATUS_FEEDBACK_BURST` | `20/h` / `5` | per widget key |
| `STATUS_FEEDBACK_IP_RATE` / `STATUS_FEEDBACK_IP_BURST` | `5/h` / `3` | per client IP, across all sites |
| `STATUS_FEEDBACK_UPLOAD_TTL` | `10m` | presigned PUT lifetime (**must be < `UNCLAIMED_TTL`**) |
| `STATUS_FEEDBACK_VIEW_TTL` | `5m` | presigned GET lifetime |
| `STATUS_FEEDBACK_UNCLAIMED_TTL` | `24h` | sweep threshold **and** the GC's minimum object age |
| `STATUS_FEEDBACK_MIN_DWELL_MS` | `3000` | minimum time between a ticket being issued and a submission |
| `STATUS_RESEND_API_KEY` | — | Resend API key for email notifications. **Optional**: without it the service runs without email (a development deployment logs what it would have sent), and the dashboard's Notifications page says so |
| `STATUS_MAIL_FROM` | `tilcer status <status@tilcer.cz>` | sender; ⚠ its domain must be **verified in Resend** |
| `STATUS_PUBLIC_URL` | `https://status.tilcer.cz` | where the dashboard is served — every link in an email is built on it |
| `STATUS_NOTIFY_DIGEST_WINDOW` | `2m` | how long the first queued notification waits for company before its email goes out (0–1h) |
| `STATUS_NOTIFY_MAX_PER_HOUR` | `6` | emails per rolling hour (1–60). Reaching it **delays**, never drops: what waits goes out together |
| `LITESTREAM_ENABLED` | `true` | R2 replication (set `false` for the local harness) |
| `LITESTREAM_R2_ENDPOINT` / `LITESTREAM_R2_BUCKET` / `LITESTREAM_ACCESS_KEY_ID` / `LITESTREAM_SECRET_ACCESS_KEY` | — | R2 creds (prefix `status/`) |

> **Note on env naming.** This service follows the `home`/`fin` fleet convention: service-owned config
> is `STATUS_`-prefixed, and sessions are random tokens stored SHA-256-hashed in the DB (there is no
> `SESSION_SECRET`). The illustrative names in `handoff/v2/PRD.md §9` (`AUTH_SITE_ID`, `SESSION_SECRET`)
> are superseded by the vars above.

---

## Deploy (Coolify — two apps, one origin)

Two separate Coolify apps, both on `status.tilcer.cz`; Traefik path-routes (longer prefix wins):

- **`status-backend`** — API-only Go image. Domain `status.tilcer.cz/api`; port **112**; Base Directory
  `/` (context root), Dockerfile `/backend/Dockerfile`; health check `/readyz`; persistent volume at
  `/data`. ⚠ **Strip Prefix must be OFF** — routes are served under `/api`. `httpx.StripAPIPrefix`
  re-prefixes a stripped path as a defensive fallback should the toggle be flipped back, deriving the
  segments it accepts from the routes the router registers; it is a safety net for a misconfigured
  proxy, not a supported routing mode.
- **`status-frontend`** — static Nginx SPA. Domain `status.tilcer.cz` (catch-all); Base Directory
  `/frontend`, Dockerfile `/frontend/Dockerfile`. It also serves the feedback widget at
  `/widget/v1.js` (one year, immutable) with `/widget/latest.js` 302-ing to it from an **exact**
  location, so the regex block that caches every `.js` for a year cannot pin "latest". ⚠ That server
  block sets `charset utf-8`: without it a cross-origin `<script>` is decoded as windows-1252 and
  every Czech string in the widget becomes mojibake, in what it shows and in what it sends.

**Prerequisite (auth provisioning).** In `auth.tilcer.cz`, a superuser must (1) create a **site**
`status` and (2) create a **service client bound to it**, copying the one-time secret into
`STATUS_AUTH_SERVICE_SECRET`. `STATUS_AUTH_JWT_SECRET` must equal auth's JWT secret. Grant Karel's
admin user access to site `status`.

Backup: Litestream replicates the SQLite DB to Cloudflare R2 under prefix `status/`; a fresh build
restores from R2 on first boot (`docker-entrypoint.sh`).

**Feedback attachments (v3).** A second, private R2 bucket — `ws-tilcer-status-feedback` — holds the
images and clips attached to user reports, under the `feedback/` prefix. Three things about it:

- ⚠ **Its token is its own.** Scope the R2 API token to that bucket alone; it must not reach the
  Litestream bucket, which holds the whole database backup.
- ⚠ **The bucket needs its own CORS policy**, because the browser PUTs to R2 directly rather than
  through this service: allow `PUT` from the origins in `STATUS_ALLOWED_ORIGINS` (**never `*`** on a
  bucket that accepts writes) with `Content-Type` as the allowed header — and not `Content-Length`,
  which a browser sets itself and never asks for in a preflight. Without it the upload fails in the
  browser with no useful error and no server-side signal at all.
  [`docs/widget.md`](docs/widget.md) §8 records the exact policy.
- **It is deliberately not backed up.** The durable record is the report text, which is in SQLite and
  already replicated. Losing an attachment loses convenience, not the record — which is why the
  nightly sweep is constrained as tightly as it is (it aborts and deletes nothing if its listing
  fails, and never touches an object younger than `STATUS_FEEDBACK_UNCLAIMED_TTL`).

`backend/spike-r2-presign.py` is the reproducer for what a presigned PUT actually enforces on R2
(V3-D54); run it by hand against a scratch bucket if that ever needs re-checking.

**Email notifications.** Set `STATUS_RESEND_API_KEY`, then turn them on — and choose the recipients,
the kinds and the muted sites — under **Notifications** in the dashboard; the settings live in SQLite,
not in env. They cover a crash group's first error-or-worse event **in production** (`environment`
`prod`, `production` or unset — see [`docs/integration.md`](docs/integration.md)), a resolved group
reopened by a new event, a new feedback report, a site turning red, and its next passing check.

- ⚠ **Verify the sender's domain in Resend first** (`tilcer.cz`, for the default
  `status@tilcer.cz`). An unverified domain is a 403 on every send; the dashboard's **Send test
  email** shows Resend's reason, and a digest refused that way keeps retrying — for up to 23 hours —
  so fixing the domain delivers it.
- **Nothing is lost to a restart or an outage.** Notifications are queued in SQLite inside the
  transaction that caused them and sent afterwards by a worker, retried with the same idempotency
  key until Resend accepts them.
- **What leaves for Resend** is an excerpt: a crash's title and the first 300 characters of its
  message (never a stack), a report's first 300 characters and its ref (never the reporter's name,
  page, browser, console or IP). Resend keeps what it sends.
- **Quota.** Resend's free plan is 100 emails a day, and the account is shared with the fleet's
  other senders; the digest window and the hourly cap exist to stay well inside it.
- An outage that began while notifications were off (or its site muted) stays silent at both ends —
  there is no "back up" for a "down" nobody was told about.
