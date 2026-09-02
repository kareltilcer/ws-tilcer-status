# Engineering Handoff — Status v3 (Feedback)

> For: **Claude Code** · Owner: Karel · Written 2026-09-02
> **Source of truth:** `PRD.md` **§V3-1…§V3-11** (behaviour, data model, acceptance criteria) and `openapi.yaml` **0.3.0** (the contract — implement it exactly). Scope rationale lives in `V3-feedback-brief.md`; where it and the PRD disagree, **the PRD wins** and says so. `../../CLAUDE.md` conventions are inherited. This is the build plan, not a re-spec.
>
> ⚠ **Read §2 before anything else.** `HANDOFF-engineering.md` (v2) is wrong in six places, and the service you are extending does not match the service that document describes.

## 1. Scope

Add a **fourth module**, `feedback`: users of the monitored apps file bug reports from inside them, with image/video attachments in a new R2 bucket, and Karel triages them in a cross-site inbox. Ship an embeddable widget (`/widget/v1.js`). **First**, repair two v2 defects that v3 would otherwise inherit.

Three PRs, in order (**V3-D57**). status has **no PRs at all** today — one commit built the entire service — and v3 is too big to repeat that.

## 2. ⚠ Where `HANDOFF-engineering.md` (v2) is now wrong

That document is the **pre-build** plan from 2026-08-01. `PRD.md` §12 (§V2-12) is the as-built record and wins. The six places it will actively mislead you:

| v2 handoff says | Reality |
|---|---|
| "two modules — `monitoring`, `crash` — over a shared `sites` registry" | **Three modules.** `sites` is a full module that **owns the entire schema** (one migration, all five tables); `monitoring` and `crash` return `Migrations() == nil`. Plus net-new `scheduler` and `retention` packages. |
| "Ports: backend 112, frontend **155**" | BE 112 is right. **FE is port 80** in the Nginx image; "155" was never a container port — only the compose harness maps 1155→80. |
| "**Do not enable Strip Prefix**" | Correct as intent, **but production has it enabled** — that is why `/api/meta` 404s. PR 1 settles it. |
| "`SESSION_SECRET`", "`PORT`", "`AUTH_SITE_ID`" | None exist. Config is **all `STATUS_`-prefixed**; sessions are random tokens SHA-256-hashed in the DB. |
| "No BE→BE shared secret in v1" | **False.** Mode B requires `STATUS_AUTH_SERVICE_SECRET` **and** `STATUS_AUTH_JWT_SECRET`. |
| "admin endpoints require `bearerAuth` (JWT) with `sessionCookie` fallback" | **The SPA has never sent a bearer JWT.** It is a session cookie plus a double-submit CSRF token, with an Origin/Referer allow-list. `openapi.yaml` 0.3.0 removes `bearerAuth` entirely. |
| "Suggested layout: `cmd/server/`, `internal/httpx/`, `web/`" | Actual: `backend/cmd/status/`, `backend/internal/platform/*`, `frontend/`, `backend/openapi.yaml`. |

## 3. Conventions this version inherits

- Go **1.26**, `chi` v5, `modernc.org/sqlite` CGO-off, **Goose**, migrations assembled in `internal/bootstrap` purely by numeric filename prefix (platform `02xxx`, sites `10xxx`, **feedback `20xxx`**).
- ⚠ **`SetMaxOpenConns(1)`** + WAL + `foreign_keys` as a DSN pragma. Never query while an outer `rows` cursor is open; inside `WithTx` use the `tx` for every read *and* write.
- All timestamps use the one fixed-width UTC `timeutil.Layout` so **string comparison is a valid time order**. Cursors depend on it. Never `time.RFC3339Nano`.
- `registry.Module` = `Name()` + `RegisterRoutes(chi.Router)` + `Migrations() fs.FS`.
- Config fails fast, **listing every problem at once**, and cross-validates.
- Errors are the uniform `Error` schema. Pagination is a keyset cursor, limit clamped 1..200, default 50.
- Frontend: React 19 + Vite 8 + TS 6 + TanStack Query 5 + sonner. ⚠ **No Tailwind, no shadcn/ui** — inline styles + oklch custom properties in `src/theme/globals.css`, dark by default via a single `.light` class on `<html>`.

---

## 4. PR 1 — the repairs

Independently deployable, and it makes browser crash reporting work **for the first time** whatever subsequently happens to v3. Nothing in it depends on the feature.

### 4.1 Strip Prefix — settle it, and stop hand-maintaining the list

1. **Turn Strip Prefix OFF** in Coolify for the backend app. This is what `README.md` and `CLAUDE.md` have always instructed; production disagrees with them.
2. **Keep `httpx.StripAPIPrefix`** as a defensive layer against the toggle being flipped back — but **derive its re-prefix set from the routes the router actually registers**, rather than the hand-written `{"/auth/", "/sites", "/crashes", "/ingest/"}`. Walk the composed `chi.Router` (`chi.Walk`) once at construction, collect the distinct first path segments under `/api`, and match against that.
3. **`TestStripAPIPrefix` must enumerate every top-level API prefix the router registers** and assert each is handled. The existing test enumerated exactly the four prefixes somebody remembered in August, which is precisely why `/meta` shipped broken.

⚠ A defensive layer that needs manual maintenance is not defensive — it is a second place to be wrong. If deriving the set proves awkward, **delete the middleware instead**; that is a better outcome than keeping the hand-written list.

### 4.2 CORS — the missing middleware

`httpx.NewRouter` mounts exactly `StripAPIPrefix`, `RequestID`, `Logger`, `Recover`. There is no CORS anywhere, no `Access-Control-*` header written by any code path, and no `OPTIONS` handler. See PRD FR-25 for why that means `clients/js/status-report.js` has never worked from another origin.

- Apply CORS to the **public group only** — `MountAuth` and `MountPublicAPI`. The gated group stays same-origin.
- Allowed origins come from the **existing** `cfg.AllowedOrigins` (`STATUS_ALLOWED_ORIGINS`, default `https://*.tilcer.cz`), reusing `auth.originMatches` for the wildcard. **Do not introduce a second origin list** (V3-D50).
- Echo the matched origin, never `*`. `Vary: Origin` on every response, matched or not.
- ⚠ **`Access-Control-Allow-Credentials` is never sent.** These endpoints authenticate by key, not cookie; sending it is the difference between a public ingest endpoint and a cross-origin door into a session.
- Allowed headers: `Content-Type`, `X-Ingest-Key`, `X-Widget-Key`. Allowed methods: `GET`, `POST`, `OPTIONS`. `Access-Control-Max-Age` around 600.

⚠ **The preflight gotcha.** A middleware alone is not enough: `OPTIONS /api/ingest/{siteId}` matches no registered route, so chi falls through to `r.NotFound`, which returns a JSON 404 — and depending on where the middleware sits it may not run at all. **Register explicit `OPTIONS` handlers** on every public path (the three auth routes, crash ingest, the three widget routes) that return 204 with the CORS headers. Do not rely on the middleware intercepting an unrouted method.

**Tests:** a preflight from an allow-listed origin returns 204 with the echoed origin; from a foreign origin it does not; no response anywhere carries `Allow-Credentials`; and an actual `POST` from an allow-listed origin carries `Access-Control-Allow-Origin`. None of this is observable from a same-origin test, which is why the bug survived 34 test functions.

### 4.3 `openapi.yaml` → 0.3.0

Already written — copy `services/status/openapi.yaml` (Nextcloud) to `backend/openapi.yaml` and keep them byte-identical from here on, as `home` v9 does. It validates as OpenAPI 3.1 with every local `$ref` resolving: 22 paths, 32 schemas.

**Do not let the build drift from it again.** This is the fourth time (home v7, home v8, status v2) that a build shipped without updating its contract.

---

## 5. PR 2 — the `feedback` module

### 5.1 Package shape

```
backend/internal/feedback/
  module.go        registry.Module: Name, RegisterRoutes (gated), MountPublic (public), Migrations
  http.go          the five admin handlers
  widget.go        the three public handlers (config / submit / claim)
  store.go         sqlite: reports, attachments, site config, tickets
  ref.go           Crockford base32 ref generation
  ticket.go        HMAC issue + single-use verify
  sweep.go         the nightly job
  blob/            the R2 client behind an interface (see 5.4)
  migrations/20001_feedback.sql
```

⚠ **`feedback` mounts twice** (V3-D52). `registry.Module.RegisterRoutes(r)` receives the **authenticated** router only; the public widget routes go through `httpx.Deps.MountPublicAPI`, exactly as `crash` mounts `POST /api/ingest/{siteId}`. Compose both in `cmd/status/main.go`.

`bootstrap.MigrationSources()` gains `{Name: "feedback", FS: feedback.MigrationsFS}`.

### 5.2 The board count crosses a module boundary

`sites` may not import `feedback`. Define in `sites` a one-method interface:

```go
type ReportCounter interface {
    ReportCounts(ctx context.Context, siteIDs []string) (map[string]int, error)
}
```

`feedback` implements it; **`cmd/status/main.go` injects it into the sites module at composition** — never a package-level global (home's §V5-12 correction, applied here from the start). With no counter injected, `SiteSummary.open_reports` is **`null`, not `0`** (V3-D53).

### 5.3 Migration `20001_feedback.sql`

Four tables exactly per PRD §V3-5: `feedback_report`, `feedback_attachment`, `feedback_site_config`, `feedback_ticket`. Every FK is `REFERENCES site(id) ON DELETE CASCADE`. Indexes as listed — the inbox depends on `(state, created_at DESC)` and the sweep on `(state, created_at)`.

**`ref` generation:** `R-` + 4 Crockford base32 chars (alphabet without I, L, O, U) = ~1.05M values. Generate, insert, and **retry on the unique violation** up to ~5 times rather than pre-checking; at household volume a collision is vanishingly rare and a pre-check is a race.

### 5.4 The R2 client

Use **`aws-sdk-go-v2`** (`service/s3` + `s3.NewPresignClient`) against the R2 endpoint with `region: "auto"`. Pin the version.

**Put it behind a small interface in `feedback/blob`** — `PresignPut`, `PresignGet`, `Head`, `Delete`, `List` — so the unit tests do not need R2. See §7 for what the fake must do.

⚠ **Never call any of these inside a transaction or with an open `rows` cursor.** `SetMaxOpenConns(1)` means one network round-trip holds the service's only writer for the length of someone else's TCP timeout. Collect what you need, close the cursor, commit, then talk to R2.

### 5.5 Presigning — what was measured, and what it means

**Probed against the real bucket 2026-09-02** (`spike-r2-presign.py`; keep it in the repo as the reproducer):

| Probe | Result |
|---|---|
| Does the SDK sign it? | `content-length;content-type;host` — yes |
| Signed 1 KiB, sends 1 KiB | 200, 1 024 stored |
| Signed 1 KiB, declares + sends 64 KiB | **403 SignatureDoesNotMatch** |
| Signed 1 KiB, **declares 1 KiB, sends 64 KiB** | **200 — 1 024 stored** |
| Nothing signed but type, sends 64 KiB | 200, **65 536 stored** |
| Signed `image/png`, sends `video/mp4` | **403 SignatureDoesNotMatch** |

So: set `ContentLength` and `ContentType` on the `PutObjectInput` before presigning, at the **clamped** size. The bucket cannot be filled beyond the signed size, and the content-type allow-list is enforced by R2 rather than by us.

⚠ **The mechanism is truncation, not refusal.** R2 reads exactly `Content-Length` bytes, discards the rest, answers **200**. Two consequences you must not design around wrongly:

1. **The claim step's size check is a presence-and-cap check, not an integrity check.** An object matching its declared length may be the first 1 KiB of a 64 KiB file. Say so in a comment where the check runs, or someone will later "improve" it into a guarantee it cannot make.
2. **Probe 4 is the danger.** Drop `ContentLength` from the presign call — as a simplification, or through an SDK bump — and the bucket becomes an open upload endpoint **with no error anywhere**. Ship a test that asserts `X-Amz-SignedHeaders` contains `content-length` **before any upload is attempted**. That assertion is the only thing standing between this design and an unbounded public write endpoint.

### 5.6 The guard chain

`POST /api/ingest/{siteId}/feedback` evaluates in this fixed order, and a table-driven test enumerates it:

**404** unknown site → **401** bad widget key → **403** disabled → **403** origin → **429** over rate → **413** body over `MAX_TEXT_BYTES` → **422** invalid → **202**

⚠ **The body is never read before the key is checked** — the v2 ingest principle, unchanged. Use a `http.MaxBytesReader` before the decode, with `DisallowUnknownFields` and trailing-content rejection, mirroring `crash`.

Keys: `wk_` + 32 random url-safe bytes, SHA-256 stored, **constant-time compared** — copy `crash`'s handling exactly, including never logging the plaintext.

### 5.7 Tickets

`GET …/feedback/config` issues one: an id, an issue time, and an HMAC over both under `STATUS_FEEDBACK_TICKET_SECRET`, plus the site id. Submission rejects a ticket that is younger than `MIN_DWELL_MS`, older than 30 minutes, already spent, or signed for a different site. Rows are deleted on use and swept by the daily job.

This is the only thing that makes the dwell check real — a client-sent `dwell_ms` is a number the client chooses.

### 5.8 Rate limiting

Two token buckets: per widget key and per client IP. **Reuse the existing limiter** from crash ingest and the existing `clientIP` helper governed by `STATUS_TRUSTED_PROXY_COUNT` (V3-D23). A second, differently-behaved XFF parser in one binary is how one of them ends up wrong. `429` carries `Retry-After`.

Store only `ip_hash` (SHA-256 of IP + `STATUS_IP_HASH_SALT`). **The IP itself never reaches a row.**

### 5.9 Deletion, and the sweep

**Order is normative** (V3-D05): collect object keys **inside** the transaction, commit, **then** delete from R2. The reverse order destroys attachments of a report that still exists if the commit fails. A failed delete leaves an orphan; the sweep is the backstop; the response does not wait on R2.

`DELETE /api/sites/{id}` needs the same treatment — the SQL cascade cannot reach the bucket.

The daily job at `STATUS_DAILY_JOB_AT` currently composes **rollup → purge**; add the sweep **last** (V3-D25) — it is the only step that touches the network and must not delay the two that keep the database honest.

⚠ **The sweep aborts on any listing error and deletes nothing** (V3-D27). "The listing came back empty, so delete everything with no live row" is how a bucket is quietly emptied. It deletes only under the `feedback/` prefix and never an object younger than `UNCLAIMED_TTL`.

⚠ **`RETENTION_DAYS` does not apply to reports.** `TestRetentionDoesNotPurgeFeedback` exists because the purge is a natural place for a later contributor to add a fourth table by symmetry.

### 5.10 Config

Per PRD §V3-9, all `STATUS_`-prefixed, using the existing loader idioms (`rateDefault` is a **per-second float** — `STATUS_INGEST_RATE=1.0` means 60/min — with a separate burst). When `STATUS_FEEDBACK_ENABLED` is true, every R2 var is **required**. Cross-validate `UPLOAD_TTL < UNCLAIMED_TTL` and `MAX_VIDEO_MB >= MAX_IMAGE_MB`.

⚠ **The R2 token must be scoped to `ws-tilcer-status-feedback` alone and must not reach the Litestream bucket** (V3-D21). This is home's D214 lesson applied before it costs anything.

---

## 6. PR 3 — the widget and the inbox

### 6.1 Building `/widget/v1.js`

**A separate Vite config**, not a second entry in the SPA build — the SPA emits hashed filenames and the widget needs a fixed one.

`frontend/vite.widget.config.ts`: library mode, `formats: ["iife"]`, `name: "StatusFeedback"`, `fileName: () => "v1.js"`, `outDir: "dist/widget"`, **`emptyOutDir: false`**, no externals. Run it after the SPA build in the Dockerfile. Target under 15 kB gzipped.

**No React, no framework.** Vanilla DOM in a **closed shadow root**. The SPA's React 19 is not a dependency the monitored apps should inherit, and the widget must survive a host page that already has a different React.

### 6.2 Nginx

```nginx
location = /widget/latest.js {          # exact match beats the regex block
  return 302 /widget/v1.js;
  add_header Cache-Control "public, max-age=300";
}
```

⚠ Two facts about the existing `frontend/nginx.conf`: a regex block matches **every** `.js` and serves it from root with **no fallback**, so a missing `/widget/v99.js` already 404s correctly — **keep the `.js` extension, never an extensionless widget path.** But that same block stamps `expires 1y; Cache-Control: public, immutable` on every `.js`, `latest.js` included, which would pin "latest" for a year. Hence the exact location above.

### 6.3 The widget contract

```html
<script src="https://status.tilcer.cz/widget/v1.js"
        data-site="home" data-key="wk_…" data-lang="cs"
        data-reporter="Kája" data-position="bottom-right" defer></script>
```

- **Fetches config before rendering anything.** Disabled site, unknown key, or a network failure ⇒ **no launcher at all** (V3-D35). Never a button that fails when pressed.
- `data-lang` falls back to `document.documentElement.lang`, then `cs`. Both string sets ship in the bundle.
- **`StatusFeedback.open()`** is public API (V3-D56); `data-launcher="none"` suppresses the floating button but not the API. Part of the `v1` contract: may gain arguments, never lose them.
- Uploads run **sequentially** (V3-D38), one retry per file, then abandoned; the claim proceeds without it.
- ⚠ **Never throw into the host app** (V3-D37). Wrap every entry point — the `DOMContentLineLoaded` handler, the click handlers, the fetch chain. A widget that crashes `home` while reporting a bug in `home` would be a small masterpiece.

### 6.4 ⚠ Content Security Policy — a new integration requirement

**Not previously recorded anywhere, and it will silently break the widget on any host that sets a CSP.** The host app must allow:

- `script-src https://status.tilcer.cz` — the bundle itself
- `connect-src https://status.tilcer.cz` — config, submit, claim
- `connect-src https://<account>.r2.cloudflarestorage.com` — ⚠ **the PUT goes to R2 directly, not through status**, so the host's CSP must name the bucket endpoint. This is the one that will be missed.

If a host uses a custom R2 domain later, that origin replaces the endpoint here.

✅ **Checked 2026-09-02 — nothing in the fleet sends a CSP today**, at either layer: no `add_header Content-Security-Policy` in any repo's `nginx.conf`, no `<meta http-equiv>`, and a live same-origin fetch at `home.tilcer.cz` and `fin.tilcer.cz` returns no CSP, no report-only variant, and **no security headers at all** beyond Cloudflare's own. So this blocks no integration PR.

⚠ **It is a tripwire, not a task.** The day someone adds a CSP to `home`, `fin` or `karel` — a reasonable thing to want — the widget breaks with a console error the reporter never sees and **no server-side signal whatsoever**: no failed request reaches status, so nothing appears in the logs or the inbox. Put all three directives in `docs/widget.md` so whoever adds a policy has them to hand.

(`home` does send `Content-Security-Policy: sandbox` on served images, PDFs and chat content — a per-response sandbox on a served file, unrelated to the document policy, and irrelevant to the widget. Do not mistake those greps for a document CSP.)

### 6.5 Dashboard screens

Per `HANDOFF-design-v3.md`. Routes `/reports` and `/reports/:ref`; query keys `['reports', filters]`, `['report', ref]`, `['site', id, 'feedback-config']`; mutations invalidate the inbox and the board. Follow **status's** stack — inline styles + oklch tokens, no Tailwind.

---

## 7. Testing & acceptance

Map to **PRD §V3-11**. Beyond the obvious:

- ⚠ **The R2 fake must TRUNCATE, not refuse.** A fake that returns 403 for an oversized body encodes behaviour R2 does not have, and every claim-step test then passes against a fiction. Model probe 3: accept the request, store `min(len(body), signedLength)` bytes, return 200.
- **One opt-in integration test** against the real bucket, gated by an env var and skipped otherwise — home's `TestV9MigrationOnRestoredCopy` pattern. No minio container in CI.
- **The signed-headers assertion** (§5.5) runs before any upload test.
- **Guard-chain order** as a table test, including that the body is unread on a bad key.
- **Preflight tests** from an allow-listed and a foreign origin (§4.2).
- **`ComputeColor` is untouched** — a test named for the fact, because someone reading "unread reports" next to `cached_color` in the same handler will eventually wire them together.
- **Deletion ordering** — assert no R2 call happens inside a transaction. Structurally if you can (the fake records whether a tx is open), by review if you cannot.
- **`internal/apitest`** over the real router with the dev bypass, as v2 does, for the five end-to-end paths.

## 8. Definition of done

All PRD §V3-11 criteria pass. `openapi.yaml` is 0.3.0 in `backend/` and byte-identical to the Nextcloud copy. `GET /api/meta` returns 200 in production. A cross-origin crash from `clients/js/status-report.js` lands. Litestream is untouched and still replicating `status/`. The R2 token reaches only its own bucket. `docs/widget.md` exists, including the CSP requirements and the bucket's CORS policy.

⚠ **And one thing no automated criterion can establish:** v3 ships **no integration** (V3-D47), so every test above can pass with no real report ever filed. **File one by hand from a real second origin** — key, preflight, presign, PUT, claim, inbox, presigned view, delete — before calling this done. home v9's `StorageBlobs` bug compiled, passed every test, and reported 0 B with an empty listing: *"it was found by opening the page."*

## 9. Deferred — defaults set, safe to proceed

`feedback_ticket` as a table (an in-process map is defensible; the table lets the sweep and limiter see it) · the inbox as a fifth nav item vs a board tab · the rate-limit numbers, which are guesses from a household's shape and one env var each · whether `/widget/latest.js` earns its Nginx exception with three embeds under one person's control. None block implementation. See PRD §V3-10.
