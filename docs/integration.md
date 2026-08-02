# Crash ingest — integration reference

The full contract is [`../backend/openapi.yaml`](../backend/openapi.yaml). This is the practical
reference for wiring a service up to report crashes.

## Endpoint

```
POST https://status.tilcer.cz/api/ingest/{siteId}
```

- `{siteId}` — the site's user-supplied id (e.g. `fin`), created in the dashboard.
- The endpoint is **public** and authenticated per-site by the ingest key; no session is needed.

## Headers

| Header | Required | Value |
|---|---|---|
| `X-Ingest-Key` | yes | the site's ingest key (`ik_…`). Shown once on create; rotatable. |
| `Content-Type` | yes | `application/json` |

The **browser** ingest key is public by design (like a Sentry DSN): it can only POST crashes for one
site, and the endpoint is rate-limited and size-capped. Backend keys should still be kept in env/secrets.

## Payload (`CrashReport`)

```jsonc
{
  "message":     "TypeError: cannot read properties of undefined (reading 'total')", // REQUIRED
  "level":       "error",              // fatal | error | warning   (default: error)
  "stack":       "at compute (app.js:42)\n…",
  "environment": "prod",               // free-form, e.g. prod | dev | staging
  "release":     "fin@2026.31.2",      // free-form release identifier
  "fingerprint": "checkout-total-nil", // optional grouping override (see below)
  "context":     { "route": "/api/summary", "method": "GET", "user_tier": "pro" },
  "occurred_at": "2026-08-01T17:26:35Z" // RFC3339; server uses receipt time if absent
}
```

| Field | Type | Notes |
|---|---|---|
| `message` | string (**required**) | The error text. Drives grouping and the group title (first ~200 chars). |
| `level` | enum | `fatal` (crashed the process — panics), `error` (handled but wrong — the default), `warning` (noise worth watching). A group tracks the **highest** level seen. |
| `stack` | string | Stack trace. The **first frame** contributes to the default fingerprint. |
| `environment` | string | Convention: `prod` / `dev` / `staging`. Shown per event. |
| `release` | string | Convention: `<site>@<version>`, e.g. `fin@2026.31.2`. |
| `fingerprint` | string | Overrides grouping — see below. |
| `context` | object | Free-form tags/metadata. Bounded by the total body cap. |
| `occurred_at` | RFC3339 | Client event time. Absent → server receipt time. |

## Grouping & fingerprinting

Similar crashes are collapsed into one **group** so counts are meaningful.

- **Default:** `hash(site_id + level + normalize(message) + first_stack_frame)`. Normalization strips
  volatile tokens — numbers, hex, UUIDs, memory addresses, quoted strings, and paths — so
  `user 41 not found` and `user 99 not found` land in the same group, and `main.go:42` groups with
  `main.go:99`.
- **Override:** supply a stable `fingerprint` to force grouping yourself (e.g. one group per feature
  area). It is honored verbatim.

A new event on a **resolved** group reopens it (regression) unless the server has
`STATUS_REOPEN_ON_REGRESSION=false`. An **ignored** group is never auto-reopened.

## Responses & error handling

| Status | Meaning | Client action |
|---|---|---|
| `202` | Accepted — `{ "group_id": N, "event_id": M }` | done |
| `401` | Missing/invalid `X-Ingest-Key` | fix the key; do not retry blindly |
| `404` | Unknown `{siteId}` | fix the id |
| `413` | Payload too large (> `STATUS_MAX_INGEST_BYTES`, default 64 KB) | trim `stack`/`context` and drop |
| `422` | Invalid body (missing `message`, bad `level`/`occurred_at`) | fix the payload |
| `429` | Rate limit exceeded (per site) | honor `Retry-After`; back off, then **drop** rather than queue unboundedly |

**Rate limit:** a per-site token bucket (default `60/min`, burst `120`). On `429` the response carries
`Retry-After` (seconds). Recommended client behaviour: retry once after the delay, otherwise drop the
event — a monitoring client must never build an unbounded backlog.

**Fail safe:** treat *any* ingest error (including network failures) as non-fatal. Never let reporting
block a request path or crash the app. The copy-in helpers already do this.

## Copy-in helpers

- **Go:** [`../clients/go/statusreport`](../clients/go/statusreport) — `Report(err, …)` + `defer Recover()`.
- **Browser:** [`../clients/js/status-report.js`](../clients/js/status-report.js) — hooks
  `window.onerror` + `unhandledrejection`, posts with `fetch({ keepalive: true })`.

## Wire up an existing `ws-tilcer-*` service (checklist)

1. In the dashboard, **Add site** with the service's id; copy the ingest key (once).
2. Add env vars to the service in Coolify:
   `STATUS_INGEST_URL=https://status.tilcer.cz/api/ingest/<id>` and `STATUS_INGEST_KEY=ik_…`
   (optionally `STATUS_ENVIRONMENT`, `STATUS_RELEASE`).
3. Copy [`clients/go/statusreport`](../clients/go/statusreport) into the service (or `go get` it).
4. In `main`: `sr, _ := statusreport.NewFromEnv(); defer sr.Recover()`; call `sr.Report(err, …)` where
   you handle errors.
5. (Optional) Set the site's **Monitor URL** to the service's `/readyz` so uptime is tracked too.
6. Trigger a test error and confirm it appears on the board.
