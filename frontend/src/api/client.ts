// The API fetch wrapper (Mode B). Same-origin: paths are relative ("/api/..."),
// so no CORS; in dev Vite proxies /api to Go.
//
// Authorization is the status SESSION COOKIE — sent automatically with
// credentials:'include'. There is NO bearer token in JS. State-changing requests
// carry the double-submit CSRF token read from the (JS-readable) `csrf` cookie. On
// 401 we hand off to the login screen (roles are refreshed server-side, so there
// is no client-side token refresh).

let onUnauthorized: (() => void) | null = null

/** setUnauthorizedHandler registers what to do on a 401 (route to login). */
export function setUnauthorizedHandler(fn: (() => void) | null): void {
  onUnauthorized = fn
}

/** csrfToken reads the double-submit token from the `csrf` cookie (not HttpOnly). */
function csrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)csrf=([^;]+)/)
  return m ? decodeURIComponent(m[1]) : ''
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

export class ApiError extends Error {
  status: number
  code: string
  detail?: string
  constructor(status: number, code: string, detail?: string) {
    super(detail ? `${code}: ${detail}` : code)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.detail = detail
  }
}

export interface RequestOptions {
  method?: string
  body?: unknown
  signal?: AbortSignal
  // skipAuthRedirect leaves a 401 to the caller (the auth endpoints themselves).
  skipAuthRedirect?: boolean
}

export async function apiFetch<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? 'GET'
  const headers: Record<string, string> = {}
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
  if (!SAFE_METHODS.has(method)) {
    const t = csrfToken()
    if (t) headers['X-CSRF-Token'] = t
  }

  const res = await fetch(path, {
    method,
    credentials: 'include',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    signal: opts.signal,
  })

  if (res.status === 401 && !opts.skipAuthRedirect) {
    onUnauthorized?.()
    throw new ApiError(401, 'unauthorized')
  }

  if (!res.ok) {
    let code = 'error'
    let detail: string | undefined
    try {
      const parsed = (await res.json()) as { error?: string; detail?: string }
      code = parsed.error ?? code
      detail = parsed.detail
    } catch {
      // non-JSON error body
    }
    throw new ApiError(res.status, code, detail)
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}
