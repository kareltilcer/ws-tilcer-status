# The feedback widget — integration reference

Everything a monitored app needs to let the people using it write to Karel. The contract is
[`../backend/openapi.yaml`](../backend/openapi.yaml) (tag `widget`); this is the practical guide,
and it is meant to be sufficient on its own.

> ⚠ Three things here have no error message anywhere when they are wrong: the **CSP directives**
> (§7), the **bucket's CORS policy** (§8) and the **`Content-Type` charset** (§9). Each fails inside
> the reporter's browser, with nothing reaching status — no failed request, no log line, no report.
> If the widget "does nothing", start there.

---

## 1. The embed

```html
<script src="https://status.tilcer.cz/widget/v1.js"
        data-site="home"
        data-key="wk_…"
        data-lang="cs"
        defer></script>
```

Paste it once, anywhere in the page. `defer` is recommended and not required.

| Attribute | Required | Meaning |
|---|---|---|
| `data-site` | **yes** | The site id as it exists in status (`home`, `fin`, …). |
| `data-key` | **yes** | That site's widget key (`wk_…`), issued once when feedback is enabled. |
| `data-lang` | no | `cs` or `en`. Falls back to `<html lang>`, then to **`cs`**. |
| `data-reporter` | no | A display label for whoever is logged into the host app ("Kája"). It is a **label, not an identity** — status never verifies it. |
| `data-release` | no | The host app's version, e.g. `home@2026.35.1`. Shown in the report's context block. |
| `data-position` | no | `bottom-right` (default), `bottom-left`, `top-right`, `top-left`. |
| `data-launcher` | no | `none` suppresses the floating button and **nothing else** — `StatusFeedback.open()` still works. |

The widget calls back to the origin its own `src` came from, so a staging copy talks to staging and
nothing has to name the API host twice.

### The floating launcher and your own bottom bar

The launcher sits 16 px from the corner, plus the device's safe-area inset. It does **not** try to
guess the height of a host app's own bottom navigation. If your app has one, either move the
launcher (`data-position="top-right"`) or suppress it and use your own trigger:

```html
<script src="https://status.tilcer.cz/widget/v1.js"
        data-site="home" data-key="wk_…" data-launcher="none" defer></script>

<button onclick="window.StatusFeedback && window.StatusFeedback.open()">Nahlásit problém</button>
```

## 2. `StatusFeedback.open()`

The one public API. It is defined from the moment the script executes — before the configuration
round-trip finishes — so a menu item wired to it never has to care about timing. A call made too
early opens the dialog as soon as the config arrives; a call made when feedback is off for the site
does nothing at all and throws nothing.

It is part of the `v1` contract: **it may gain arguments, it will never lose one.**

Guard it with `window.StatusFeedback &&` anyway. If the script was blocked — by a CSP, an ad
blocker, or a network failure — the global is not there, and a host app should not throw over it.

## 3. What the widget sends

Always:

- the page URL and the referrer;
- the viewport size, the browser's locale and its user agent;
- `data-reporter` and `data-release`, if the embed set them;
- the message the person typed, the kind they chose, and their attachments.

Only when **console capture is enabled for that site** (off by default — see §5):

- the last 50 lines of console output, each capped at 200 characters and the whole tail capped to
  about 3 kB;
- the last uncaught JavaScript error.

The dialog **shows the reporter all of it before they submit**, and the console tail has its own
opt-out checkbox. Nothing is collected from the console until the site has opted in: the widget does
not patch `console` at all otherwise.

⚠ The client IP is **not** stored. status hashes it with a deployment salt for rate limiting and
throws the address away.

## 4. Limits

| | |
|---|---|
| Message | 4 000 characters |
| Attachments | 3 per report |
| Images | `image/png`, `image/jpeg`, `image/webp`, `image/gif` — up to 10 MB each |
| Video | `video/mp4`, `video/webm` — up to 50 MB each |

Defaults; a deployment can change them with `STATUS_FEEDBACK_*` (see the README). The widget reads
the live values from the config endpoint and states them in the dialog.

⚠ **An oversized file is refused by the widget, not by status.** The upload URL is signed for the
exact size the widget declared, so a file over the cap could only be signed at the cap and then
rejected by R2 with a signature error the reporter would never see explained. Refusing it in the
dialog — with the file named and the limit stated — is the only place that failure can be made
legible.

## 5. Turning it on, and the console-capture decision

In the dashboard: **site detail → User feedback**.

1. Flip **Feedback enabled**. The widget key is issued at that moment and shown **once** — only its
   SHA-256 is stored, so it genuinely cannot be recovered later. Copy the snippet from the modal.
2. Leave **Send console output** off unless the app needs it.

⚠ **Why console capture is off by default.** `home` has a privacy model in which a member's private
notes are unreadable by anyone, admins included. A console line printed by that app could carry a
private note's title into status, whose reader is Karel's admin session — a side door with a
different lock, opened by a feature nobody connected to privacy at all. It is on for `status` itself
(one user, nothing to leak) and deliberate everywhere else.

### Rotating the key

**site detail → Rotate widget key.** The old key dies immediately, so every page still embedding it
stops showing the launcher until its snippet is updated — the widget renders nothing rather than a
button that fails.

⚠ Rotating the **widget** key never touches the **ingest** key. That is why they are separate: a
spammed widget can be revoked without silencing that site's crash reporting.

## 6. Versioning and caching

| Path | Cache | Meaning |
|---|---|---|
| `/widget/v1.js` | `public, max-age=31536000, immutable` | The v1 contract, frozen for its life. |
| `/widget/latest.js` | 302 → `/widget/v1.js`, `max-age=300` | Follows the current major. |

Pin `v1.js`. A breaking change becomes `/widget/v2.js` and every existing embed keeps working.

⚠ **What `immutable` costs, since your users are the ones who pay it.** The filename is fixed and
unhashed, so a browser that has loaded the widget once will not issue even a conditional request for
a year. A non-breaking fix to v1 therefore cannot reach it: the repair ships as `v2.js` and your
embed has to be updated to see it. That is the trade PRD FR-24 chose, and it is one line of
`frontend/nginx.conf` to revisit.

⚠ **Keep the `.js` extension.** Nginx serves `/widget/*.js` from the filesystem with no SPA
fallback, so an unknown version 404s. An extensionless path would fall through to the SPA and hand
your page `index.html` with a 200.

## 7. ⚠ Content Security Policy — the requirement most likely to be missed

If the host app sends a CSP, it must allow **three** things, and the third is the trap:

```
script-src  https://status.tilcer.cz
connect-src https://status.tilcer.cz
connect-src https://<account>.r2.cloudflarestorage.com
```

The third one exists because **the file upload goes directly to R2, not through status**. A policy
that allows only the status origin produces a widget that opens, accepts a file, and fails at upload
with a console error the reporter never sees — and **no server-side signal at all**: no request
reaches status, so nothing appears in its logs or its inbox.

✅ Measured 2026-09-02: no site in the fleet sends a document CSP, so this blocks nothing today. It
is a **tripwire**, not a task — the day anyone adds one to `home`, `fin` or `karel`, this list is
what they need in front of them.

(`home` does send `Content-Security-Policy: sandbox` on served images, PDFs and chat content. That
is a per-response sandbox on a served file, unrelated to the document's script/connect policy, and
it does not affect the widget.)

## 8. ⚠ The bucket's own CORS policy

The browser PUTs attachments straight to R2, so the **bucket** needs a CORS policy of its own. This
lives in the Cloudflare dashboard, not in this repository, and it is written down here rather than
remembered:

```json
[
  {
    "AllowedOrigins": ["https://home.tilcer.cz", "https://fin.tilcer.cz", "https://status.tilcer.cz"],
    "AllowedMethods": ["PUT"],
    "AllowedHeaders": ["content-type"],
    "MaxAgeSeconds": 3600
  }
]
```

⚠ **Never `"AllowedOrigins": ["*"]`.** The presigned URL is the only thing standing between the
bucket and an open upload endpoint; a wildcard hands any page on the internet the ability to complete
an upload with a lifted URL. Add each host origin explicitly, matching `STATUS_ALLOWED_ORIGINS`.

`Content-Length` is not listed because a browser sets it itself and refuses to let script do so —
which is also why the widget must reject an oversized file before asking for a slot (§4).

## 9. ⚠ Serve the bundle with a charset

`/widget/v1.js` must be served as `text/javascript; charset=utf-8`. The status origin already does
(`charset utf-8` in `frontend/nginx.conf`), and the bundle is additionally built ASCII-only so that
it survives a proxy or CDN that strips the parameter.

This matters because a **cross-origin classic script does not inherit the host document's encoding**.
Without a charset the browser falls back to windows-1252, every Czech string in the widget becomes
mojibake, and the widget then *sends* the corrupted text — so the report in the inbox is corrupt too,
with nothing anywhere to say why. If you mirror the bundle somewhere else, keep the header.

## 10. Failure modes, and where to look

| Symptom | Cause |
|---|---|
| No launcher at all | Feedback is off for the site, the key is wrong, or the config request failed. **By design** — the widget never renders a button that fails when pressed. |
| Launcher appears, sending fails with "Příliš mnoho pokusů" | Rate limit (≈20 reports/hour per key, ≈5/hour per IP). `Retry-After` drives the wait shown. |
| Report lands, file does not | The PUT failed twice. The report keeps its text and the dashboard shows the attachment as `missing`. Check the bucket's CORS policy (§8) and the host's CSP (§7). |
| Czech renders as `OdeÅ¡le se takÃ©` | The bundle is being served without a charset (§9). |
| Nothing in the console, nothing in status | A CSP is blocking the script or the connection (§7). |

The widget **never throws into the host app**. Every entry point is wrapped, and every failure is
silent except the dialog's own error states — so an exception in your app's console is not the
widget's.

## 11. Trying it locally

```bash
cd frontend && npm run build     # emits dist/widget/v1.js
```

Serve `dist/` on one port and a host page on another to exercise the real cross-origin path, and run
the backend with the host's origin in `STATUS_ALLOWED_ORIGINS`. A same-origin test proves nothing
here: CORS, the CSP and the charset are all cross-origin-only failures.
