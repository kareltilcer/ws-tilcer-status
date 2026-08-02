# status — fleet monitoring & crash reporting

`status.tilcer.cz` is an internal ops dashboard that shows, at a glance, whether every service on
the droplet is healthy (**🟢 green / 🟠 orange / 🔴 red / ⚪ unknown**), combining active uptime
polling with crash reports the other services send in.

- **Backend** — a Go modular monolith (`backend/`): two modules — `monitoring` (uptime poller +
  rollups) and `crash` (public ingest + grouping) — over a shared `sites` registry. Embedded SQLite,
  Litestream → R2 backup. Serves `status.tilcer.cz/api`.
- **Frontend** — a React + Vite SPA (`frontend/`) behind Mode B auth. Serves `status.tilcer.cz`.
- **Clients** — copy-in [`clients/go`](clients/go) and [`clients/js`](clients/js) crash reporters.

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
cd backend && go test ./...
```

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
| `STATUS_ALLOWED_ORIGINS` | `https://*.tilcer.cz` | CSRF Origin allowlist |
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
| `STATUS_DAILY_JOB_AT` | `00:15` | daily rollup+purge time (UTC) |
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
  `/data`. **Do NOT enable Strip Prefix** — routes are served under `/api` or they 404.
- **`status-frontend`** — static Nginx SPA. Domain `status.tilcer.cz` (catch-all); Base Directory
  `/frontend`, Dockerfile `/frontend/Dockerfile`.

**Prerequisite (auth provisioning).** In `auth.tilcer.cz`, a superuser must (1) create a **site**
`status` and (2) create a **service client bound to it**, copying the one-time secret into
`STATUS_AUTH_SERVICE_SECRET`. `STATUS_AUTH_JWT_SECRET` must equal auth's JWT secret. Grant Karel's
admin user access to site `status`.

Backup: Litestream replicates the SQLite DB to Cloudflare R2 under prefix `status/`; a fresh build
restores from R2 on first boot (`docker-entrypoint.sh`).
