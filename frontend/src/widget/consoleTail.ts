// The console tail — the one piece of this widget that reads the host app's own
// output, and the reason the whole disclosure block exists (PRD §V3-8).
//
// ⚠ Capture is installed ONLY when the site opted in. `home` has a privacy model
// in which a member's private notes are unreadable by anyone including admins,
// and a console line could carry a private note's title into status, whose reader
// is Karel's admin session. Collecting those lines into memory for a site that
// opted out — even without ever sending them — is the same posture with an extra
// step, so nothing is patched until the config says `console_capture: true`.
//
// The cost of that ordering is that lines logged before the config answers are
// not captured. That is the right trade: the tail is what the host printed around
// the bug the reporter is describing, and they open the dialog afterwards.

const MAX_LINES = 50
const MAX_LINE_CHARS = 200

/**
 * MAX_TAIL_BYTES bounds what the tail contributes to the submission body.
 *
 * ⚠ The server caps the WHOLE JSON body at `STATUS_FEEDBACK_MAX_TEXT_BYTES`
 * (8192 by default) and answers 413 over it — while separately allowing a
 * 4 000-character message, a 2 000-character page_url and a 50 × 200 console
 * tail, which together cannot fit. The console tail is the only one of those the
 * widget generates rather than receives, so it is the one the widget trims. A
 * reporter losing the oldest console lines is invisible; a reporter losing their
 * typed report to a 413 is not.
 */
const MAX_TAIL_BYTES = 3000

type Method = 'log' | 'info' | 'warn' | 'error' | 'debug'
const METHODS: Method[] = ['log', 'info', 'warn', 'error', 'debug']

export interface ConsoleCapture {
  /** lines returns the tail, newest last, already within every cap. */
  lines: () => string[]
  /** lastError is the most recent uncaught error or rejection, or null. */
  lastError: () => string | null
}

/** emptyCapture is what a site with console capture off gets: the same shape,
 *  nothing in it, and no patched console anywhere. */
export const emptyCapture: ConsoleCapture = { lines: () => [], lastError: () => null }

/** utf8Length counts the bytes a string costs on the wire, which is what the
 *  server's body cap is denominated in — `String.length` counts UTF-16 units and
 *  would under-count every Czech sentence in the buffer. */
export function utf8Length(s: string): number {
  let n = 0
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c < 0x80) n += 1
    else if (c < 0x800) n += 2
    else if (c >= 0xd800 && c <= 0xdbff) {
      n += 4
      i++ // surrogate pair: one code point, four bytes
    } else n += 3
  }
  return n
}

/** trimToBudget drops the OLDEST lines until the tail fits the byte budget. The
 *  newest lines are the ones nearest the failure the reporter is describing.
 *
 *  ⚠ The cost is measured on the SERIALIZED line, not the raw one. `formatArg`
 *  renders object arguments with `JSON.stringify`, so a typical captured line is
 *  full of quotes — and every one of them costs a second byte once the tail is
 *  itself serialized into the body. Charging the raw length plus two quotes
 *  under-counts by however many characters need escaping, which on a tail of any
 *  size runs past the fixed slack the caller leaves and 413s a report whose text
 *  the reporter is then told, wrongly, to shorten. */
export function trimToBudget(lines: string[], budget = MAX_TAIL_BYTES): string[] {
  let total = 0
  const kept: string[] = []
  for (let i = lines.length - 1; i >= 0; i--) {
    const cost = utf8Length(JSON.stringify(lines[i])) + 1 // the escaped literal, plus its comma
    if (total + cost > budget) break
    total += cost
    kept.push(lines[i])
  }
  return kept.reverse()
}

/** formatArg renders one console argument without ever throwing — a getter that
 *  throws, or a circular structure, must not take the host's console with it. */
export function formatArg(v: unknown): string {
  if (typeof v === 'string') return v
  if (v === null) return 'null'
  if (v === undefined) return 'undefined'
  if (v instanceof Error) return `${v.name}: ${v.message}`
  try {
    const s = JSON.stringify(v)
    return s === undefined ? String(v) : s
  } catch {
    try {
      return String(v)
    } catch {
      return '[unprintable]'
    }
  }
}

function truncate(s: string, max: number): string {
  return s.length <= max ? s : s.slice(0, max)
}

/**
 * installConsoleCapture patches the console and the two global error events,
 * keeping the last MAX_LINES lines. It is idempotent per page: a second call
 * returns the first capture rather than double-wrapping the console.
 */
let installed: ConsoleCapture | null = null

export function installConsoleCapture(): ConsoleCapture {
  if (installed) return installed
  const buffer: string[] = []
  let lastError: string | null = null

  const push = (level: Method, args: unknown[]) => {
    try {
      const prefix = level === 'log' ? '' : `[${level}] `
      const text = truncate(prefix + args.map(formatArg).join(' '), MAX_LINE_CHARS)
      buffer.push(text)
      if (buffer.length > MAX_LINES) buffer.shift()
    } catch {
      // A capture failure is never allowed to change what the host app logs.
    }
  }

  for (const level of METHODS) {
    const original = console[level] as (...args: unknown[]) => void
    if (typeof original !== 'function') continue
    console[level] = function patched(...args: unknown[]) {
      push(level, args)
      // ⚠ The original is called last and unconditionally: the host's own logging
      // must behave identically whether or not the widget is on the page.
      original.apply(console, args)
    } as typeof console.log
  }

  window.addEventListener('error', (e) => {
    try {
      const err = (e as ErrorEvent).error
      const stack = err instanceof Error && err.stack ? err.stack.split('\n').slice(0, 3).join('\n') : ''
      const where = (e as ErrorEvent).filename
        ? ` — ${(e as ErrorEvent).filename}:${(e as ErrorEvent).lineno}:${(e as ErrorEvent).colno}`
        : ''
      lastError = truncate(stack || `${(e as ErrorEvent).message}${where}`, MAX_LINE_CHARS)
    } catch {
      // ignore
    }
  })

  window.addEventListener('unhandledrejection', (e) => {
    try {
      const reason = (e as PromiseRejectionEvent).reason
      lastError = truncate(`Unhandled rejection: ${formatArg(reason)}`, MAX_LINE_CHARS)
    } catch {
      // ignore
    }
  })

  installed = {
    lines: () => trimToBudget(buffer.slice()),
    lastError: () => lastError,
  }
  return installed
}
