// The three public routes the widget speaks to, plus the direct PUT to R2.
//
// ⚠ The upload does not go through status. The browser PUTs straight to the
// bucket with the presigned URL it was handed, which is why a host app's Content
// Security Policy has to name the R2 endpoint in `connect-src` as well as the
// status origin (V3-D58) — see docs/widget.md.

import type { Accepted, Submission, UploadSlot, WidgetConfig } from './types'

export interface ApiTarget {
  /** base is the origin the widget bundle was served from. */
  base: string
  site: string
  key: string
}

const CONFIG_TIMEOUT_MS = 10_000
const SUBMIT_TIMEOUT_MS = 20_000
const CLAIM_TIMEOUT_MS = 15_000

/** UPLOAD_STALL_MS ends an upload that has stopped moving.
 *
 *  ⚠ Not a total timeout: a 50 MB video on household wifi legitimately takes
 *  minutes, and `xhr.timeout` would abort it. A half-open TCP connection, on the
 *  other hand, produces no progress event, no error and no end — which would
 *  otherwise leave the dialog on its spinner forever, with the claim never run.
 *  Silence, not duration, is what is being bounded. */
const UPLOAD_STALL_MS = 45_000

function ingestPath(t: ApiTarget, suffix: string): string {
  return `${t.base}/api/ingest/${encodeURIComponent(t.site)}/feedback${suffix}`
}

/** withTimeout aborts a fetch that never answers. A hung config request must end
 *  as "no launcher" rather than as a page that quietly waits forever. */
function withTimeout(ms: number): { signal: AbortSignal; done: () => void } {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), ms)
  return { signal: ctl.signal, done: () => clearTimeout(timer) }
}

/**
 * fetchWidgetConfig asks what the widget may do, and hands back the single-use
 * ticket a submission needs.
 *
 * ⚠ It never throws its own failure into the caller as a rejected promise the
 * caller has to remember to catch — anything other than a 200 with a usable body
 * resolves to `{ enabled: false }`, which is exactly the "render nothing" case
 * (V3-D35). A disabled site, an unknown key, a 429 and a dead network are the
 * same outcome for the reporter: no button that fails when pressed.
 */
export async function fetchWidgetConfig(t: ApiTarget): Promise<WidgetConfig> {
  const { signal, done } = withTimeout(CONFIG_TIMEOUT_MS)
  try {
    const res = await fetch(ingestPath(t, '/config'), {
      method: 'GET',
      headers: { 'X-Widget-Key': t.key },
      signal,
      // ⚠ No credentials: these endpoints authenticate by key, and the server
      // never sends Access-Control-Allow-Credentials (V3-D43).
      credentials: 'omit',
      cache: 'no-store',
    })
    if (!res.ok) return { enabled: false }
    const body = (await res.json()) as WidgetConfig
    return body && typeof body.enabled === 'boolean' ? body : { enabled: false }
  } catch {
    return { enabled: false }
  } finally {
    done()
  }
}

export type SubmitResult =
  | { ok: true; accepted: Accepted }
  /** rate is a 429; retryAfterSeconds is the server's own Retry-After, which is
   *  readable cross-origin only because NewCORS exposes that header. */
  | { ok: false; kind: 'rate'; retryAfterSeconds: number }
  /** invalid is a 422 — something in the payload, not the connection. */
  | { ok: false; kind: 'invalid' }
  /** tooLarge is the 413 from the whole-body cap, and it is the ONE send failure
   *  the reporter can act on: it is their own text that did not fit, so it must
   *  not be dressed up as a dropped connection they can only retry into. */
  | { ok: false; kind: 'tooLarge' }
  /** refused is 401/403/404: the embed's key or configuration is wrong, which is
   *  Karel's problem and not something the reporter can act on. */
  | { ok: false; kind: 'refused' }
  | { ok: false; kind: 'network' }

export async function submitReport(t: ApiTarget, body: Submission): Promise<SubmitResult> {
  const { signal, done } = withTimeout(SUBMIT_TIMEOUT_MS)
  try {
    const res = await fetch(ingestPath(t, ''), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Widget-Key': t.key },
      body: JSON.stringify(body),
      signal,
      credentials: 'omit',
    })
    if (res.status === 202) {
      const accepted = (await res.json()) as Accepted
      if (!accepted || typeof accepted.ref !== 'string') return { ok: false, kind: 'network' }
      return { ok: true, accepted: { ref: accepted.ref, uploads: accepted.uploads ?? [] } }
    }
    if (res.status === 429) {
      const header = Number(res.headers.get('Retry-After'))
      return { ok: false, kind: 'rate', retryAfterSeconds: Number.isFinite(header) && header > 0 ? header : 300 }
    }
    if (res.status === 413) return { ok: false, kind: 'tooLarge' }
    if (res.status === 422) return { ok: false, kind: 'invalid' }
    if (res.status === 401 || res.status === 403 || res.status === 404) return { ok: false, kind: 'refused' }
    return { ok: false, kind: 'network' }
  } catch {
    return { ok: false, kind: 'network' }
  } finally {
    done()
  }
}

/** ForbiddenUploadHeaders are the header names the browser sets itself and
 *  refuses to let script set.
 *
 *  ⚠ `Content-Length` is signed into the URL and IS sent — by the browser, from
 *  the File's own size. Passing it to setRequestHeader would be ignored with a
 *  console warning, which is why the widget must refuse an oversized file before
 *  it ever gets a slot: the signature is computed from the size the widget
 *  declared, and the browser will send the size the file actually is. */
const FORBIDDEN_UPLOAD_HEADERS = ['content-length']

export interface Upload {
  promise: Promise<boolean>
  abort: () => void
}

/** putObject uploads one file to its presigned URL, reporting progress. It
 *  resolves false rather than rejecting: a failed attachment never fails a
 *  report. */
export function putObject(slot: UploadSlot, file: Blob, onProgress: (pct: number) => void): Upload {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<boolean>((resolve) => {
    let watchdog: ReturnType<typeof setInterval> | null = null
    const settle = (ok: boolean) => {
      if (watchdog !== null) clearInterval(watchdog)
      watchdog = null
      resolve(ok)
    }
    try {
      xhr.open('PUT', slot.url, true)
      for (const [name, value] of Object.entries(slot.headers ?? {})) {
        if (FORBIDDEN_UPLOAD_HEADERS.includes(name.toLowerCase())) continue
        xhr.setRequestHeader(name, value)
      }
      let movedAt = Date.now()
      xhr.upload.onprogress = (e) => {
        movedAt = Date.now()
        if (e.lengthComputable && e.total > 0) onProgress(Math.min(99, Math.round((e.loaded / e.total) * 100)))
      }
      xhr.onload = () => settle(xhr.status >= 200 && xhr.status < 300)
      xhr.onerror = () => settle(false)
      xhr.onabort = () => settle(false)
      xhr.ontimeout = () => settle(false)
      xhr.send(file)
      watchdog = setInterval(() => {
        if (Date.now() - movedAt < UPLOAD_STALL_MS) return
        // abort() fires onabort, which settles the promise and clears this timer.
        try {
          xhr.abort()
        } catch {
          settle(false)
        }
      }, 5_000)
    } catch {
      settle(false)
    }
  })
  return { promise, abort: () => { try { xhr.abort() } catch { /* already finished */ } } }
}

/** claimUploads tells the server which objects actually landed. Its failure is
 *  survivable by design: an unclaimed attachment stays `pending` until the
 *  nightly sweep resolves it, and the report keeps its text either way. */
export async function claimUploads(t: ApiTarget, ref: string): Promise<void> {
  // ⚠ Timed out like every other call here. The claim is awaited by the dialog
  // before the success screen, so a request that never answers is a spinner that
  // never ends — and the sweep resolves an unclaimed attachment anyway.
  const { signal, done } = withTimeout(CLAIM_TIMEOUT_MS)
  try {
    await fetch(ingestPath(t, `/${encodeURIComponent(ref)}/claim`), {
      method: 'POST',
      headers: { 'X-Widget-Key': t.key },
      signal,
      credentials: 'omit',
    })
  } catch {
    // Nothing to do and nothing to say: the sweep is the backstop.
  } finally {
    done()
  }
}
