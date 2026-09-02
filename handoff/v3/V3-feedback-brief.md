# v3 brief — Feedback (`feedback` · the widget · R2 attachments)

> **Scope frozen 2026-09-02.** Twenty questions asked, twenty answered; **no blanks left open** (§10a closes the last four). Decisions **V3-D01–V3-D49**. §12 lists four modelling choices deliberately left to the PRD.
>
> ⚠ **Superseded in three places by `PRD.md` §V3**, written 2026-09-02 against the repo. This brief is kept as frozen — a scope document rewritten afterwards stops being evidence of what was agreed when — but where the two disagree, the PRD wins and says so: **V3-D29's per-site `allowed_origins` is dropped** (V3-D50 — `STATUS_ALLOWED_ORIGINS` already exists, defaults to `https://*.tilcer.cz`, and already does wildcard matching for the CSRF origin check, so the column would be a second allow-list with different semantics) · **V3-D13's "`PATCH /api/sites/{id}` gains feedback config" becomes its own `/api/sites/{id}/feedback-config` routes** (V3-D51 — that route belongs to the `sites` module, and `feedback` owning config it cannot serve is V3-D02's boundary violation in a different costume) · and **§7's leak-table row 15 was half wrong** (V3-D33 — Nginx's `.js` regex block already 404s a missing widget path; the real trap is that the same block would cache `/widget/latest.js` for a year).
>
> ⚠ **One thing this brief found by reading the code rather than by asking:** **status has no CORS handling at all**, and therefore `clients/js/status-report.js` — a shipped v2 deliverable whose whole purpose is posting from another origin — **has never worked**, failing invisibly inside its own `catch`. §7.4 has the evidence. v3 repairs it, and V3-D44 is where that repair reaches into v3's data model.
>
> **status has never used numbered decisions.** `home` carries a single D-series across versions (now at D252); status has only section numbers and a §V2-12 as-built record. v3 starts its own series, prefixed **`V3-D`**, so a decision can be cited without colliding with home's.
>
> ⚠ **v3 is the first status version that writes bytes it does not own.** Everything the service holds today it generated itself: check results, crash payloads posted by code Karel wrote, rollups. v3 accepts free text, screenshots and video from **human beings**, through a public endpoint, into **paid object storage**. Three things follow, and they shape the whole document: the failure mode of abuse changes from "a full table" to "a full bucket"; the content can contain personal data that nobody on the receiving end asked for; and the delete path stops being `ON DELETE CASCADE`, because SQLite cannot cascade into R2.
>
> ⚠ **v3 cannot ship on top of v2's live routing defect.** `/api/meta` 404s in production today because `httpx.StripAPIPrefix`'s re-prefix allow-list is `{"/auth/", "/sites", "/crashes", "/ingest/"}` and Strip Prefix is evidently **enabled** in Coolify — the opposite of what the repo docs instruct. Every new top-level path in this brief walks into the same trap. §9 makes fixing it a prerequisite, and §5.1 explains why the *public* half of v3 deliberately hides under `/api/ingest/` where the allow-list already works.

---

## 1. What Karel asked for

In his words:

1. **v3 accepts bug reports from the users of monitored apps.**
2. **A button/dialog pair can be placed in a monitored app** for the user to report with.
3. **An image or a video can be attached** — so **a new R2 bucket** is needed.

Everything below resolves what those three sentences leave open.

---

## 2. The sixteen questions, and the answers

| # | Question | Answer |
|---|---|---|
| 1 | Where do reports live in the backend? | **A fourth module, `feedback`**, owning its own tables. `crash` fingerprinting stays machine-only — hand-written prose would poison grouping. |
| 2 | How does the widget authenticate? | **A second per-site key, `wk_`**, scoped to the feedback endpoints only and rotatable without touching crash ingest. |
| 3 | How do attachment bytes reach R2? | **Presigned PUT, browser → R2 directly.** Bytes never touch the droplet. Costs a claim step and a sweep for what is never claimed. |
| 4 | How does the widget get into an app? | **A hosted, versioned script** from the status origin — `/widget/v1.js` — rendering into a shadow root. One line per app; a widget fix never means redeploying home/fin. |
| 5 | What does the widget send beyond the user's text? | **Page context** (URL, referrer, viewport, UA, locale), **a reporter hint** supplied by the host app, and **a console tail + last JS error** — the third of these opt-in per site, for the reason in §7.2. |
| 6 | Does an open report change a site's colour? | **No.** Colour stays the machine signal. Reports are an unread count on the card and a filter. A person saying "this is confusing" must not make `home` look degraded beside a real outage. |
| 7 | What are the media limits, and is anything derived? | **Images and short video, stored exactly as uploaded.** ≤3 files, ≤10 MB per image, ≤50 MB per video. **No transcoding, no thumbnails, no ffmpeg on the droplet.** |
| 8 | What language does the dialog speak? | **Czech and English**, chosen by `data-lang` (falling back to the host's `<html lang>`, then Czech). **The admin side of status stays English** — the widget is the first translated surface in this service. |
| 9 | What stops a stranger filling the bucket? | **Four layers**: per-IP and per-key rate limits · a per-site kill switch · a honeypot plus a minimum dwell time · and the widget mounting only for signed-in users. §7 is the whole table. |
| 10 | Public sites — does `karel.tilcer.cz` get the widget? | **No. v3 is authenticated apps only** — `home`, `fin`, `status` itself. Every reporter is someone Karel knows. The public site waits for v4. |
| 11 | How long do reports and attachments live? | **Reports are kept** — a hand-written report is an artifact, not telemetry. **Attachments die with their report**, and unclaimed uploads are swept after 24 h. `RETENTION_DAYS` explicitly does **not** apply. |
| 12 | What does the reporter get back? | **A thank-you and a reference code** (`R-7QK2`), one-way. No inbox, no notifications, no reply route. |
| 13 | What does triage look like? | **One cross-site inbox**, states `new → open → resolved \| declined`, plus an internal note only Karel sees. New reports drive the unread count on the board cards. |
| 14 | How does an attachment reach Karel's screen? | **A short-TTL presigned GET**, minted per attachment when the report is opened. Video seeking works because R2 serves the range requests. |
| 15 | Is the attachment bucket backed up? | **No.** The durable record is the report text, which is in SQLite and already replicated by Litestream. Screenshots are context; losing them loses convenience. Stated as a limitation in §10, not hidden. |
| 16 | Does v3 own v2's open defects? | **Yes — fixed first, in v3's own PR.** See §9. |
| 17 | Strip Prefix — on or off? | **Off in Coolify, and `StripAPIPrefix` kept defensively** — but with its prefix set **derived from the registered routes**, because the hand-written list is the thing that failed. |
| 18 | The bucket? | **`ws-tilcer-status-feedback`**, same Cloudflare account, its own token scoped to it alone. |
| 19 | Does the dialog ask what kind of thing this is? | **Yes** — Chyba / Nápad / Něco jiného, default `bug`. It is the split that decides what happens next, and it is what makes the module `feedback`. |
| 20 | Does v3 integrate the widget into the apps? | **No — widget plus `docs/widget.md`.** The script tag lands in `home`/`fin`/`status` as its own small PR each. ⚠ Which means v3 can pass its criteria without a real report ever being filed — see V3-D47. |

---

## 3. The model

### 3.1 `feedback` is the first status module that owns a migration block

As built, `internal/sites` owns **the entire schema** — one migration, all five tables — and `monitoring` and `crash` return `Migrations() == nil`. That was defensible with two modules over one registry. It stops being defensible the moment a third functional module needs four tables of its own: `sites` would be carrying the schema of three modules it does not otherwise know about.

**V3-D01.** `feedback` declares its own migrations in block **`20xxx`**, joining `platform` (`02xxx`) and `sites` (`10xxx`). Ordering stays what it is — the numeric filename prefix, assembled in `internal/bootstrap`.

**V3-D02.** `feedback` does **not** add columns to `site`. Its per-site configuration lives in its own table (§3.3). A module that reaches into another module's table to add a column has not been separated from it.

### 3.2 The four tables

**`feedback_report`** — indexes `(site_id, created_at DESC)`, `(state, created_at DESC)`, unique `(ref)`

| col | type | notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | internal |
| `ref` | TEXT NOT NULL UNIQUE | `R-` + 4 Crockford base32 chars — what the reporter is shown and what routes use |
| `site_id` | TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE | |
| `kind` | TEXT NOT NULL DEFAULT 'bug' | `bug` \| `idea` \| `other`, **asked in the dialog** (V3-D46) |
| `message` | TEXT NOT NULL | the user's text, capped at 4 000 chars |
| `state` | TEXT NOT NULL DEFAULT 'new' | `new` \| `open` \| `resolved` \| `declined` |
| `reporter_label` | TEXT NULL | the host app's hint — **untrusted display string, never an identity** |
| `page_url` · `referrer` · `user_agent` · `viewport` · `locale` | TEXT NULL | page context |
| `app_release` | TEXT NULL | optional, mirrors `crash_event.release` |
| `console_tail` | TEXT NULL | JSON array, only when the site opted in |
| `last_error` | TEXT NULL | last `window.onerror` seen by the widget, same opt-in |
| `internal_note` | TEXT NULL | admin-only, never leaves the dashboard |
| `ip_hash` | TEXT NULL | SHA-256 of client IP + a deployment salt. **The IP itself is never stored** |
| `created_at` · `updated_at` | TEXT NOT NULL | `timeutil.Layout` |
| `resolved_at` | TEXT NULL | |

**`feedback_attachment`** — indexes `(report_id)`, `(state, created_at)`, unique `(object_key)`

| col | type | notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | |
| `report_id` | INTEGER NOT NULL REFERENCES feedback_report(id) ON DELETE CASCADE | |
| `object_key` | TEXT NOT NULL UNIQUE | `feedback/{site_id}/{ref}/{n}-{rand}.{ext}` |
| `content_type` | TEXT NOT NULL | from the signed allow-list, not from the filename |
| `byte_size` | INTEGER NOT NULL | **declared** at init, **confirmed by HEAD** at claim |
| `state` | TEXT NOT NULL DEFAULT 'pending' | `pending` \| `stored` \| `missing` |
| `created_at` | TEXT NOT NULL | |
| `claimed_at` | TEXT NULL | |

**`feedback_site_config`** — PK `(site_id)`

| col | type | notes |
|---|---|---|
| `site_id` | TEXT PK REFERENCES site(id) ON DELETE CASCADE | |
| `enabled` | INTEGER NOT NULL DEFAULT 0 | the kill switch — **off until deliberately turned on** |
| `widget_key_hash` | TEXT NOT NULL | SHA-256, constant-time compared, plaintext shown once — the `ik_` precedent exactly |
| `widget_key_set_at` | TEXT NOT NULL | |
| `allowed_origins` | TEXT NOT NULL | JSON array. **No wildcard is accepted** — see §7.1 |
| `console_capture` | INTEGER NOT NULL DEFAULT 0 | opt-in, per §7.2 |
| `created_at` · `updated_at` | TEXT NOT NULL | |

**`feedback_ticket`** — PK `(id)`, index `(expires_at)`
Single-use form tickets (§7.3). Small, self-purging, and the only reason the dwell-time check means anything.

**V3-D03.** A site with `feedback_site_config` absent has feedback **off**. Absence is the default state, not a missing row to repair.

### 3.3 The unread count crosses a module boundary, and it does so downwards

`GET /api/sites` must show an unread-report count per card, but `sites` may not import `feedback` — the dependency runs the other way, exactly as `home` guards with its catalog registry.

**V3-D04.** `sites` defines a one-method provider interface (`ReportCounts(ctx, siteIDs) map[string]int`), `feedback` implements it, and `internal/bootstrap` wires it at composition. Never a package-level global — the §V5-12 correction from home, applied here from the start. With no provider registered the count is absent, not zero, and the card renders no badge.

### 3.4 Deleting is where this version is most likely to go wrong

`ON DELETE CASCADE` removes rows. It does not remove objects from R2. Deleting a site today is one statement; after v3 it is a statement **and** a bucket operation that can fail independently.

**V3-D05.** The object keys are collected **inside** the transaction, the transaction commits, and the R2 deletes run **after** the commit. A failed delete leaves an orphan, and the orphan sweep (§6.3) is the backstop. The reverse order — delete objects, then fail the commit — destroys attachments belonging to a report that still exists.

**V3-D06.** The orphan sweep is the only thing permitted to delete an object that no row points at, it only ever deletes under the `feedback/` prefix, and it deletes nothing younger than `STATUS_FEEDBACK_UNCLAIMED_TTL`. A GC that can outrun an in-flight upload is a data-loss bug wearing a maintenance-job costume.

### 3.5 What v3 must not break

Five things v2 built that v3 sits directly on top of, each a place where this feature could quietly undo one:

**V3-D05a.** ⚠ **The database runs on `SetMaxOpenConns(1)`.** Every R2 call in this version — presigning, HEAD, DELETE, LIST — happens **outside** any transaction and outside any open `rows` cursor. A network round-trip inside `WithTx` holds the service's only writer connection for the duration of someone else's TCP timeout. This is the single easiest way to turn a feature about screenshots into an outage.

**V3-D05b.** `ComputeColor` is **not touched by v3**, and a test says so by name. Colour is the machine signal (V3-D of question 6); a future contributor reading "unread reports" beside `cached_color` in the same handler is exactly who that test is for.

**V3-D05c.** The new admin mutations go through the **session cookie + double-submit CSRF** the SPA actually uses — not `bearerAuth`, which is what `openapi.yaml` currently claims and which is part of why it is being corrected in V3-D18.

**V3-D05d.** All new timestamps use the one fixed-width UTC `timeutil.Layout`. The inbox cursor is keyset over `(created_at, id)` and depends on string comparison being a valid time order. Never `time.RFC3339Nano`.

**V3-D05e.** The inbox screens follow status's own frontend stack, which is **not** home's: inline styles plus oklch custom properties in `src/theme/globals.css`, **no Tailwind and no shadcn/ui**, dark by default with a single `.light` class on `<html>`. The widget shares none of this — it is a separate bundle with its own styles inside a shadow root, and it must render correctly inside `home`'s light Tailwind pages without inheriting or leaking a single rule.

---

## 4. The submission flow, end to end

```
host page          status backend                     R2
   │                     │                             │
   │  GET  config ──────►│  (key, origin, enabled)      │
   │◄──── config+ticket ─│                              │
   │                     │                              │
   │  POST feedback ────►│  guard chain → insert report │
   │◄──── ref + N PUT URLs (signed, exact-size)         │
   │                     │                              │
   │  PUT file 1 ───────────────────────────────────────►│
   │  PUT file 2 ───────────────────────────────────────►│
   │                     │                              │
   │  POST claim ───────►│  HEAD each object ──────────►│
   │◄──── stored/missing │◄─────────────────────────────│
```

**V3-D07.** Upload URLs are minted **only in the response to an accepted report**. There is no standalone "give me an upload URL" endpoint. No report, no URL — which is the single most effective thing in this design against bucket abuse, and it is free.

**V3-D08.** The presigned PUT signs **`Content-Type` and `Content-Length` as signed headers**, at the exact declared size, clamped server-side to the cap before signing. This is the whole enforcement mechanism: a presigned PUT cannot otherwise be size-limited, and a URL signed for 4 194 304 bytes will not accept 4 194 305. A client that lies about the size upfront gets a URL that refuses the file it actually has.

**V3-D09.** `content_type` comes from a **fixed allow-list** — `image/png`, `image/jpeg`, `image/webp`, `image/gif`, `video/mp4`, `video/webm` — matched against what the client declares, never inferred from the filename, and signed into the URL. The extension in `object_key` is derived from the allow-listed type, not from user input.

**V3-D10.** The claim step **HEADs** every object and writes the size R2 reports, not the size the client declared. An object that is absent or the wrong size is marked `missing`; the report survives without it. A report is never rejected because an attachment failed — the text is the thing worth keeping.

**V3-D11.** Claim is idempotent and requires the same widget key. A report whose claim never arrives keeps `pending` attachments; the sweep removes the objects and marks the rows `missing` after the TTL. The dashboard says so plainly rather than rendering a broken image.

---

## 5. API delta

### 5.1 The public half hides under `/api/ingest/` on purpose

`/ingest/` is already on the `StripAPIPrefix` re-prefix allow-list and demonstrably works in production — it is the path crash reporting uses. `/meta` is not on that list and 404s.

**V3-D12.** The three public routes live under the working prefix:

- `GET  /api/ingest/{siteId}/feedback/config` → `{enabled, kind_options, max_files, max_image_bytes, max_video_bytes, accept[], strings_version, ticket}` — requires `X-Widget-Key`
- `POST /api/ingest/{siteId}/feedback` → `202 {ref, uploads:[{attachment_id, url, expires_at, headers}]}`
- `POST /api/ingest/{siteId}/feedback/{ref}/claim` → `200 {attachments:[{id, state}]}`

This is defensive, not a substitute for the fix in §9 — but it means the reporting path is the half least likely to be broken by a Coolify setting.

**V3-D13.** The admin half uses new top-level paths and **therefore depends on §9 being done first**:

- `GET    /api/reports` — cross-site inbox; filters `state`, `site`, `kind`; keyset cursor, limit clamp 1..200 default 50, newest first
- `GET    /api/reports/{ref}`
- `PATCH  /api/reports/{ref}` — `state`, `internal_note`
- `DELETE /api/reports/{ref}` — deletes the row and its objects, per V3-D05
- `GET    /api/reports/{ref}/attachments/{id}/url` — mints the short-TTL presigned GET
- `POST   /api/sites/{id}/rotate-widget-key` — plaintext once, `wk_` + 32 url-safe bytes
- `PATCH  /api/sites/{id}` gains `feedback` config: `enabled`, `allowed_origins`, `console_capture`

**V3-D14.** Routes address a report by **`ref`**, not by the integer id. It is what the reporter quotes and what Karel will paste.

**V3-D15.** Role gate follows v2 exactly: any authenticated session **reads**; mutations require `admin` via `httpx.RequireAdmin` — `PATCH`/`DELETE /reports`, `rotate-widget-key`, and the site config patch. The presigned-GET mint is a **read**.

### 5.2 The guard chain, in a fixed order

**V3-D16.** `POST /api/ingest/{siteId}/feedback` evaluates, and a test enumerates, exactly this order:

**404** unknown site → **401** bad widget key → **403** feedback disabled → **403** origin not allow-listed → **429** over rate (key or IP) → **413** oversized body → **422** invalid (bad ticket, honeypot filled, dwell too short, unknown content type, too many files) → **202**.

The v2 principle carries over unchanged: **an untrusted body is never parsed before the key is checked.** Disabled-and-known is a 403 rather than a 404 because the caller already proved they hold the key; there is nothing left to conceal, and a 404 there would send Karel debugging a site id that is correct.

**V3-D17.** `429` carries `Retry-After`, and `413` is returned by the backend for the *text* payload only. An oversized *file* is refused by R2 against the signed content-length, never by the droplet — which is the point of V3-D08.

### 5.3 OpenAPI

**V3-D18.** `openapi.yaml` ships **in the same PR** at **0.3.0**, describing the built surface — including the four routes v2 never documented (`/api/auth/login`, `/api/auth/session`, `/api/auth/logout`, `/api/meta`), the per-window `buckets` default, and **cookie + double-submit CSRF instead of `bearerAuth`**. The v7/v8/v2 failure does not get a fourth outing. `home` v9 proved it is avoidable.

---

## 6. R2 — the bucket, the keys, and the jobs

### 6.1 A separate bucket, and a token that can reach nothing else

**V3-D19.** Attachments get their **own R2 bucket — `ws-tilcer-status-feedback`**, in the same Cloudflare account as the Litestream bucket, with its own API token scoped to it alone. The name follows the repo (`ws-tilcer-status`) rather than the service slug, so the bucket list stays readable as the fleet grows. Not a prefix in an existing bucket. Two reasons, and the second is the real one: the lifecycle rules differ (backups are precious, attachments are disposable), and the write path is reachable from a browser. A bucket boundary is the cheapest blast-radius boundary available.

**V3-D20.** The bucket is **private**. There is no public base URL, no `r2.dev` domain, no custom domain. Every read is a presigned GET minted by the backend under an admin session (V3-D13).

**V3-D21.** ⚠ **The application's R2 token is scoped to the attachments bucket alone and must not be able to read the Litestream bucket.** This is home's **D214** lesson applied before it costs anything: home *declined* to let its app process hold `LITESTREAM_*` credentials, on the grounds that it would widen the app's reach to the credentials for the household's entire database backup. status is about to give its app process object-storage credentials for the first time. They must be the narrow ones.

**V3-D22.** CORS on the bucket allows `PUT` from the allow-listed origins only, with `Content-Type` and `Content-Length` as allowed headers. `*` is not acceptable on a bucket that accepts writes.

### 6.2 Configuration (all `STATUS_`-prefixed, fail-fast, listing every problem)

| var | default | notes |
|---|---|---|
| `STATUS_FEEDBACK_ENABLED` | `false` | master switch. When true, **every R2 var below is required at boot** |
| `STATUS_R2_ENDPOINT` · `_BUCKET` · `_ACCESS_KEY_ID` · `_SECRET_ACCESS_KEY` | — | the narrow token of V3-D21 |
| `STATUS_FEEDBACK_MAX_FILES` | `3` | |
| `STATUS_FEEDBACK_MAX_IMAGE_MB` | `10` | |
| `STATUS_FEEDBACK_MAX_VIDEO_MB` | `50` | |
| `STATUS_FEEDBACK_MAX_TEXT_BYTES` | `8192` | the `413` boundary for the JSON body |
| `STATUS_FEEDBACK_RATE` | `20/hour`, burst `5` | per widget key |
| `STATUS_FEEDBACK_IP_RATE` | `5/hour` | per client IP across all sites |
| `STATUS_FEEDBACK_UPLOAD_TTL` | `10m` | presigned PUT lifetime |
| `STATUS_FEEDBACK_VIEW_TTL` | `5m` | presigned GET lifetime |
| `STATUS_FEEDBACK_UNCLAIMED_TTL` | `24h` | sweep threshold, and the GC's minimum object age |
| `STATUS_FEEDBACK_MIN_DWELL_MS` | `3000` | |
| `STATUS_FEEDBACK_TICKET_SECRET` | — | required when enabled |
| `STATUS_IP_HASH_SALT` | — | required when enabled |

**V3-D23.** Client IP comes from the **existing** `STATUS_TRUSTED_PROXY_COUNT` machinery (default `1` = Coolify's lone Traefik) that already governs the login rate limit. A second, differently-behaved XFF parser in the same binary is how one of them ends up wrong.

**V3-D24.** Cross-validation at boot, alongside the existing `ROLLUP_RETENTION_DAYS >= max(...)` check: `UPLOAD_TTL < UNCLAIMED_TTL`, and `MAX_VIDEO_MB >= MAX_IMAGE_MB`. Fail fast, listing every problem, as the service already does.

### 6.3 Two jobs, joining the existing scheduler

**V3-D25.** The daily job at `STATUS_DAILY_JOB_AT` currently composes **rollup → purge**. It gains a third step, **last**: `rollup → purge → feedback sweep`. Ordering is deliberate — the sweep is the only step that can talk to the network, and it must not be able to delay the two that keep the database honest.

**V3-D26.** The sweep does two things: marks `pending` attachments older than the TTL as `missing` and deletes their objects; then lists the `feedback/` prefix and deletes objects older than the TTL that match no live row — home's orphan-reconciliation pattern, minus the reporting page.

**V3-D27.** ⚠ **The sweep never runs while the process cannot reach R2.** A listing that returns empty because of a credential error, followed by "delete everything with no live row", is inverted into "delete nothing" — the sweep aborts on any listing error and logs it. This is the failure mode that quietly empties a bucket.

**V3-D28.** Retention explicitly does **not** apply to reports. `RETENTION_DAYS` purges `check_result` and `crash_event`; a test named for the fact (`TestRetentionDoesNotPurgeFeedback`) asserts it, because the purge is a natural place for someone to add a fourth table by symmetry.

---

## 7. Where feedback can be abused, or can leak

Treat this table as a floor, not a census. v9's grew from eighteen rows to twenty-three under review, and the five it gained were the ones nobody thought of.

| # | Surface | Handling |
|---|---|---|
| 1 | The widget key sits in HTML any signed-in user can read | ⚠ **Auth-only mounting is friction, not a boundary.** A household member can lift the key and post from anywhere. The origin allow-list, the rate limits and the kill switch are what actually bound the damage; the key's job is to name the site, not to prove innocence |
| 2 | Origin spoofing | An `Origin` header is trivially forged outside a browser. The allow-list stops a *page* on another site from using the key; it stops nothing at a shell prompt. Layered with rate limits deliberately |
| 3 | Bucket flooding | No report, no upload URL (V3-D07); exact-size signing (V3-D08); ≤3 files per report; per-key and per-IP rate limits; kill switch |
| 4 | A single huge file | Refused by R2 against the signed `Content-Length`, before a byte reaches the droplet |
| 5 | Scripted spam | Honeypot field + minimum dwell time, both enforced through the single-use ticket (§7.3) |
| 6 | Report text | Capped at 4 000 chars, stored as text, **never rendered as HTML** in the dashboard |
| 7 | `reporter_label` | An untrusted string from the host app. Displayed, escaped, and never joined to anything |
| 8 | `page_url` | Can carry query-string secrets (a reset token, a session id in a badly built app). Stored as sent; **never made clickable in the inbox without an explicit action** |
| 9 | ⚠ **Console tail** | See §7.2 — the one real cross-service privacy interaction in this version |
| 10 | Screenshots and video | Can contain anything on the reporter's screen, including other people's data. Access-controlled to Karel's admin session, presigned for 5 minutes, deletable per report. **Not encrypted**, and the brief does not pretend otherwise |
| 11 | Client IP | Hashed with a deployment salt and stored as `ip_hash`. The IP is used for rate limiting in memory and never written to a row |
| 12 | Presigned GET URLs | A bearer token for their lifetime — anyone Karel forwards one to can open it until it expires. 5 minutes, and the dashboard never puts one in a shareable link |
| 13 | Enumeration of `ref` | Refs are short and admin routes are gated, so `ref` is a convenience code, not a secret. It is never a route on the public half — the claim route takes it **with the widget key** |
| 14 | The claim route | Same key, same rate limiter, idempotent, and it can only ever move attachments belonging to the ref it names |
| 15 | ⚠ **The SPA fallback swallowing `/widget/*`** | An unknown widget path served `index.html` with a **200** means the host app executes HTML as JavaScript. Nginx must 404 unknown `/widget/*` explicitly (§8) |
| 16 | ⚠ **CORS — confirmed absent** | **v2 has no CORS handling at all** (verified against `main`, 2026-09-02). See §7.4: browser crash reporting has never worked cross-origin, and v3 must ship the middleware v2 lacks |

### 7.1 Origins are declared, never wildcarded

**V3-D29.** `allowed_origins` is a JSON array of exact origins per site, and the config rejects `*`. The preflight response echoes only a matching origin. A site with an empty array has feedback effectively off — and the dashboard says so rather than letting Karel wonder why the button does nothing.

### 7.2 The console tail is opt-in, and `home` is the reason

**V3-D30.** `console_capture` defaults to **off** and is enabled per site, deliberately.

The reason is specific. `home` v9 built a privacy model in which a member's private notes and documents are unreadable **by anyone, admins included** — the refusal is a 404, never a 403, so an id cannot even be confirmed. A console tail shipped from a `home` page could carry a private note's title into `status`, where the reader is Karel's admin session. That is not a hypothetical bypass of v9's model; it is a side door with a different lock, and it would be opened by a feature nobody connected to privacy at all.

So: off by default, on per site with the consequence understood, capped at **50 lines × 200 chars**, and the dialog **shows the reporter what will be sent** before they submit. A person who can see what they are attaching can decline to attach it.

### 7.3 The ticket, because dwell time is otherwise theatre

**V3-D31.** `GET …/feedback/config` issues a **single-use ticket** — an id, an issue time, an HMAC over both with `STATUS_FEEDBACK_TICKET_SECRET`. Submission requires it, and the server rejects a ticket that is younger than `MIN_DWELL_MS`, older than 30 minutes, already spent, or not signed for that site.

A `dwell_ms` field sent by the client is a number the client chooses. The ticket makes the check real, rate-limits dialog *opens* as well as submissions, and gives the widget a natural place to learn it has been switched off. `feedback_ticket` rows are deleted on use and swept by the daily job.

### 7.4 ⚠ CORS does not exist, and v2's browser crash client has never worked

**Verified against `main` on 2026-09-02 by reading the source.** `httpx.NewRouter` mounts exactly four middlewares — `StripAPIPrefix`, `RequestID`, `Logger`, `Recover`. There is **no CORS middleware, no `Access-Control-*` header written anywhere in the backend, and no `OPTIONS` handler**. The only three mentions of CORS in the entire repository are frontend comments explaining that the SPA is same-origin and therefore *doesn't need it*:

> `frontend/nginx.conf`: *"The SPA calls the API host-relative … so there is no nginx proxy and no CORS."*

That reasoning is correct for the dashboard and wrong for the thing next to it. `clients/js/status-report.js` — the shipped, documented v2 deliverable whose entire purpose is to post from **another** origin — sends both `Content-Type: application/json` and `X-Ingest-Key`. Neither is CORS-safelisted, so every call is preflighted. The `OPTIONS` request matches no route, falls to `r.NotFound`, and returns a JSON **404 with no `Access-Control-Allow-Origin`**. The browser blocks the POST.

**The report never leaves the page.** And it fails invisibly, because the client is deliberately fail-safe:

```js
}).catch(function () {
  /* fail safe: drop ingest errors */
});
```

**V3-D41.** This is a **v2 defect, not a v3 requirement discovered late**: §11's criterion *"copy-in Go helper and browser JS snippet … verified to land a crash end-to-end"* is not merely untested (as §V2-12 records) — for the browser half it is **false**, unless the page was served from `status.tilcer.cz` itself. The Go helper is unaffected; it is not a browser.

**V3-D42.** v3 ships the CORS middleware v2 lacks, mounted on the **public group only** — `/api/auth/*` and `/api/ingest/*`. The gated group stays same-origin: the dashboard is served from the status origin and nothing else may reach it with credentials.

**V3-D43.** The allowed origin is resolved **per site from `feedback_site_config.allowed_origins`** (V3-D29), echoed back only on a match, `Vary: Origin` always. `Access-Control-Allow-Credentials` is **never** sent — these endpoints authenticate by key, not by cookie, and sending it would be the difference between a public ingest endpoint and a cross-origin door into a session.

**V3-D44.** ⚠ **Crash ingest needs the same treatment, and it has no `feedback_site_config` row to read.** Either the origin allow-list moves onto `site` where both modules can reach it, or `crash` gets its own. The brief's V3-D02 (feedback does not touch `site`) is stated for feedback's *config*; an allow-list serving two modules is a **`sites`-owned** concern and belongs there. Resolve this when drafting §V3 of the PRD — it is the one place where fixing v2's bug reshapes v3's data model.

**V3-D45.** A test posts a preflight to `/api/ingest/{siteId}` from an allow-listed origin and from a foreign one, and asserts the headers on both. Nothing about this bug would have been caught by a same-origin test suite, which is exactly why it survived a build with 34 test functions.

---

## 8. The widget

**V3-D32.** `frontend/src/widget/` is a **second Vite entry**, built to `/widget/v1.js` as an IIFE. **No React, no framework** — vanilla DOM in a shadow root, target under 15 kB gzipped. The SPA's React 19 is not a dependency the monitored apps should inherit, and the widget must survive being embedded in a page that already has a different React.

**V3-D33.** Versioned path, permanent contract. `/widget/v1.js` is served `Cache-Control: public, max-age=31536000, immutable`; `/widget/latest.js` is a 302 to the current major with `max-age=300`. A breaking change becomes `/widget/v2.js` and every existing embed keeps working.

⚠ **Two specifics from reading `frontend/nginx.conf` (2026-09-02), because the config as it stands gets one of them right by accident and the other wrong:**

- The SPA fallback is `location / { try_files $uri $uri/ /index.html; }`, but a **regex block matches every `.js` path** and serves it from root with no fallback. So a missing `/widget/v99.js` already 404s correctly — the leak-table row 15 trap applies to an *extensionless* widget path, not a `.js` one. Keep the extension; do not invent `/widget/v1`.
- That same regex block stamps **`expires 1y; Cache-Control: public, immutable`** on every `.js`, `/widget/latest.js` included — which would pin "latest" for a year and defeat its entire purpose. Nginx matches an exact `=` location before any regex, so `location = /widget/latest.js` must be declared explicitly with the 302 and `max-age=300`. Without it the redirect is silently cached forever by every host app.

**V3-D34.** The embed is one line, and everything is a data attribute:

```html
<script src="https://status.tilcer.cz/widget/v1.js"
        data-site="home"
        data-key="wk_…"
        data-lang="cs"
        data-reporter="Kája"
        defer></script>
```

`data-lang` falls back to `document.documentElement.lang`, then to `cs`. `data-reporter` is optional and passed straight through as the untrusted label of §7's row 7.

**V3-D35.** The widget **fetches config before it renders anything**. Disabled site, unknown key, or a network failure means **no launcher button at all** — never a button that fails when pressed.

**V3-D36.** Shadow root, closed. All styles inside it. The launcher is a fixed-position button, bottom-right by default, overridable by `data-position`. Focus trap in the dialog, `Escape` closes, restores focus to the launcher, and the dialog is keyboard-reachable — this is the surface with the most non-technical users in the entire fleet.

**V3-D37.** The widget **never throws into the host app.** Every entry point is wrapped; every failure is silent except for the dialog's own error state. The v2 helpers set this rule for the crash clients ("both fail safe — never crash the host app on ingest failure") and a widget that crashes `home` while reporting a bug in `home` would be a small masterpiece.

**V3-D38.** Upload progress is shown per file, uploads run **sequentially** (a 50 MB video and two screenshots in parallel on household wifi is worse than in series), and a failed PUT is retried once before the claim proceeds without it.

**V3-D39.** Czech and English string sets ship **inside the bundle**. No runtime translation fetch, no third endpoint, and `strings_version` in the config response exists only so a mismatch is diagnosable.

### 8.1 Czech vocabulary (fixed)

| English | Czech |
|---|---|
| Report a problem | **Nahlásit problém** |
| What went wrong? | **Co se pokazilo?** |
| Attach a screenshot or video | **Přiložit snímek nebo video** |
| Send | **Odeslat** |
| Thanks — your report reached us | **Děkujeme — hlášení dorazilo** |
| Reference | **Číslo hlášení** |
| Bug / Idea / Something else | **Chyba / Nápad / Něco jiného** |
| This will also send | **Odešle se také** |
| File is too large | **Soubor je příliš velký** |
| Too many attempts, try again later | **Příliš mnoho pokusů, zkuste to později** |

Plurals are needed for *1 soubor · 2 soubory · 5 souborů* and *1 hlášení · 2 hlášení · 5 hlášení*. MB and kB do not inflect.

---

## 9. What v3 must fix before it can ship

**V3-D40.** These are v3's, not backlog items, and they land **first in v3's own PR**:

1. ⚠ **Strip Prefix goes OFF in Coolify — and `StripAPIPrefix` stays, defensively.** Turn the setting off so the backend receives `/api/*` as written, and keep the normalizer as a belt-and-braces layer against someone flipping the toggle back. Keeping it has one condition attached: the allow-list is what failed, so it must stop being a hand-written list. The middleware's re-prefix set is **derived from the routes the router actually registers**, and `TestStripAPIPrefix` enumerates every top-level API prefix rather than the four somebody remembered in August. `/meta` and `/reports` fall out of that automatically; so does the next one. A defensive layer that needs manual maintenance is not defensive — it is a second place to be wrong.
2. ⚠ **`openapi.yaml` to 0.3.0** describing the real surface (V3-D18).
3. **Decide about `/healthz` and `/readyz` at the public origin.** They serve the SPA shell today, so status cannot monitor itself through its own URL. v3 does not have to fix it, but it must stop being an unrecorded surprise.
4. ⚠ **Ship CORS on the public group** (§7.4, V3-D41–D45). This is not preparation for v3 — it repairs a v2 deliverable that has never worked, and it is the item most likely to change v3's data model (V3-D44).

---

## 10. Worked cases the implementation must reproduce

1. **The ordinary report.** Kája, signed in to `home`, clicks *Nahlásit problém*, types two sentences, attaches a phone screenshot, sends. She sees `R-7QK2`. Karel's board shows `home` **still green** with a `1` badge; the inbox shows one `new` report with a reporter label of "Kája", the page URL, and a viewable screenshot.
2. **The abandoned dialog.** She opens it, attaches a 40 MB video, closes the tab before submitting. **Nothing was uploaded** — no report, no URL. Zero bytes in the bucket.
3. **The half-finished upload.** She submits, the video PUT fails, the claim reports it `missing`. The report is in the inbox with its text and its screenshot, and the missing attachment is stated as missing, not rendered broken.
4. **The oversized file.** A 90 MB video: the widget refuses it before any request, and if the client is patched to lie, R2 refuses the PUT against the signed length. The droplet never sees a byte either way.
5. **The kill switch.** Karel turns `fin` off in the dashboard. `fin`'s widget stops rendering a launcher on the next page load; a submission from a stale page gets **403**. `home` is unaffected, and no key was rotated.
6. **The rotation.** `rotate-widget-key` on `home` shows a new `wk_` once; the old key gets **401** immediately; `home`'s crash ingest key is untouched and crash reporting never blinks.
7. **The deletion.** Karel deletes `R-7QK2`. The row goes, both objects go, and a presigned URL minted a minute earlier stops resolving once R2 has processed the delete.
8. **The site deletion.** Deleting `fin` cascades its reports and removes their objects after the commit. An object that fails to delete is picked up by the next sweep as an orphan.
9. **The dead sweep.** R2 credentials are wrong. The sweep logs the listing error and **deletes nothing at all** — not "everything, since nothing matched".
10. **The console tail.** `home` has `console_capture` off. A report from `home` carries no console lines, and the dialog does not offer them. Turning it on for `status` itself changes only `status`.

---

## 10a. The last four decisions

**V3-D46 — `kind` is asked, not inferred.** A small segmented control in the dialog: **Chyba / Nápad / Něco jiného**, defaulting to `bug`. One tap, and it splits the inbox into *things that are broken* and *things people want* — the split that decides what happens next. It is also what earns the module the name `feedback` rather than `bugreport`. Triage may correct it; the reporter's answer is a hint, not a verdict.

**V3-D47 — v3 ships the widget and the integration doc; it does not put the widget into the apps.** The deliverable is the service, the bundle at `/widget/v1.js`, and `docs/widget.md` with the one-line embed and the key-rotation flow. Adding the script tag to `home`, `fin` and `status` is a small separate PR in each repo — which `home`'s conventions demand anyway, since a change there touches its own PRD, CHANGELOG and registry. ⚠ **This means v3 can be declared done without a single real report ever having been filed.** §11's criteria must therefore include an end-to-end pass performed by hand against a real second origin — the v2 lesson (*"it was found by opening the page"*) applied before it costs anything.

**V3-D48 — `console_capture` defaults on for `status` itself, off for `home` and `fin`.** status's dashboard has no private-item model and exactly one user, so there is nothing for a console tail to leak. It is also the natural place to prove the whole path — key, CORS preflight, presign, PUT, claim, sweep — against an app that can be broken freely.

**V3-D49 — the four blanks are closed.** Bucket `ws-tilcer-status-feedback`, same account, scoped token (V3-D19) · Strip Prefix **off**, middleware kept but its list derived rather than written (§9.1) · `kind` **ships** (V3-D46) · `console_capture` on for status only (V3-D48). What remains open is not a blank but a modelling question, and it belongs to the PRD: **V3-D44**, where the per-site origin allow-list lives once crash ingest needs one too.

---

## 11. Out of scope, explicitly

Reply threads and any route by which a reporter reads anything back · email or push notification of a new report · a public bug tracker or any unauthenticated read surface · **anonymous reporting and `karel.tilcer.cz`** (v4) · in-browser screenshot or screen capture · transcoding, thumbnails, poster frames, or ffmpeg anywhere · attachment backup, mirroring, or versioning · linking a report to a crash group · reporter accounts, real identity, or any authentication of the reporter beyond the host app's own · per-site quotas or storage accounting (home's Úložiště has no counterpart here) · translating the status admin UI · captcha of any kind · batch or offline submission · editing a report's text after submission.

## 12. What the PRD still has to settle

Nothing here blocks drafting. Each is a modelling choice better made with the whole §V3 in front of you than in a brief.

1. ⚠ **V3-D44 — where the per-site origin allow-list lives.** Feedback needs one; fixing v2's CORS bug means crash ingest needs one too. Two modules sharing a config concern makes it `sites`-owned, which is the one thing V3-D02 said feedback would not do — for its own config. Resolve deliberately, not by whichever handler gets written first.
2. **Whether `feedback_ticket` is a table or an in-process map.** The brief says table. One process, one writer connection, and a restart invalidating outstanding tickets is harmless — so a map is defensible and cheaper. The table's advantage is that the sweep and the rate limiter can see it.
3. **Whether the inbox is a new top-level nav item or a tab on the board.** The 230 px side nav collapses to a drawer on mobile and currently holds four routes; a fifth is not free.
4. **Rate-limit numbers.** `20/hour` per key and `5/hour` per IP are guesses made from a household's shape, not measurements. They are one env var each and should be revisited after a month of real use.
