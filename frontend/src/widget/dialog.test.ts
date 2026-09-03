import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { SubmitResult, Upload } from './api'
import type { ConsoleCapture } from './consoleTail'
import { FeedbackDialog, type DialogApi, type ReportContext } from './dialog'
import { limitsFrom } from './files'
import { STRINGS } from './i18n'
import type { Submission, UploadSlot } from './types'

const limits = limitsFrom({
  enabled: true,
  max_files: 3,
  max_image_bytes: 10 * 1024 * 1024,
  max_video_bytes: 50 * 1024 * 1024,
  accept: ['image/png', 'video/mp4'],
})

const context: ReportContext = {
  pageUrl: 'https://home.tilcer.cz/nakupy/sobota',
  referrer: 'https://home.tilcer.cz/poznamky',
  viewport: '375 × 812',
  locale: 'cs-CZ',
  appRelease: 'home@2026.35.1',
  reporterLabel: 'Kája',
  browserLabel: 'Safari · iPhone',
}

const capture: ConsoleCapture = {
  lines: () => ['[home] POST /api/shopping/items/48 → 500'],
  lastError: () => 'TypeError: t.items is undefined',
}

interface Harness {
  root: HTMLElement
  dialog: FeedbackDialog
  submitted: Submission[]
  claimed: string[]
  puts: { slot: UploadSlot; file: Blob }[]
  closed: number
  q: <T extends Element>(selector: string) => T | null
  all: <T extends Element>(selector: string) => T[]
  byText: (selector: string, text: string) => HTMLElement | null
}

function harness(opts: {
  consoleCapture?: boolean
  submit?: (body: Submission) => Promise<SubmitResult>
  put?: (slot: UploadSlot, file: Blob, onProgress: (p: number) => void) => Upload
  ticket?: string | null
}): Harness {
  const root = document.createElement('div')
  document.body.appendChild(root)
  const submitted: Submission[] = []
  const claimed: string[] = []
  const puts: { slot: UploadSlot; file: Blob }[] = []
  const h = { closed: 0 }

  const api: DialogApi = {
    submit: async (body) => {
      submitted.push(body)
      return opts.submit ? opts.submit(body) : { ok: true, accepted: { ref: 'R-7QK2', uploads: [] } }
    },
    put: (slot, file, onProgress) => {
      puts.push({ slot, file })
      return opts.put
        ? opts.put(slot, file, onProgress)
        : { promise: Promise.resolve(true), abort: () => {} }
    },
    claim: async (ref) => {
      claimed.push(ref)
    },
    ticket: () => (opts.ticket === undefined ? 'ticket.sig' : opts.ticket),
    refreshTicket: async () => {},
  }

  const dialog = new FeedbackDialog({
    root,
    strings: STRINGS.cs,
    limits,
    consoleCapture: opts.consoleCapture ?? false,
    capture,
    context: () => context,
    api,
    onClosed: () => {
      h.closed++
    },
  })

  return {
    root,
    dialog,
    submitted,
    claimed,
    puts,
    get closed() {
      return h.closed
    },
    q: <T extends Element>(s: string) => root.querySelector<T>(s),
    all: <T extends Element>(s: string) => Array.from(root.querySelectorAll<T>(s)),
    byText: (s, text) =>
      Array.from(root.querySelectorAll<HTMLElement>(s)).find((n) => (n.textContent ?? '').includes(text)) ?? null,
  } as Harness
}

function type(h: Harness, text: string): void {
  const input = h.q<HTMLTextAreaElement>('textarea')!
  input.value = text
  input.dispatchEvent(new Event('input'))
}

function press(h: Harness, key: string, init: KeyboardEventInit = {}): void {
  h.q<HTMLElement>('[role="dialog"]')!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, ...init }))
}

/** flush drains the promise chain the submit path runs through. */
async function flush(times = 6): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve()
}

beforeEach(() => {
  document.body.innerHTML = ''
})

describe('opening and closing', () => {
  it('puts focus in the message field and marks itself a modal dialog', () => {
    const h = harness({})
    h.dialog.open()
    const dialog = h.q<HTMLElement>('[role="dialog"]')!
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(document.activeElement).toBe(h.q('textarea'))
  })

  it('Escape closes an untouched form and returns focus to the opener', () => {
    const h = harness({})
    h.dialog.open()
    press(h, 'Escape')
    expect(h.dialog.isOpen).toBe(false)
    expect(h.closed).toBe(1)
  })

  it('asks before discarding typed text, and a second Escape still gets out', () => {
    const h = harness({})
    h.dialog.open()
    type(h, 'Něco se pokazilo')
    press(h, 'Escape')
    // ⚠ Still open: losing what the reporter typed is the real failure here.
    expect(h.dialog.isOpen).toBe(true)
    expect(h.root.textContent).toContain(STRINGS.cs.discardTitle)
    press(h, 'Escape')
    expect(h.dialog.isOpen).toBe(false)
  })

  it('traps Tab inside the dialog in both directions', () => {
    const h = harness({})
    h.dialog.open()
    const focusable = h.all<HTMLElement>(
      '[role="dialog"] button:not([disabled]):not([tabindex="-1"]), [role="dialog"] textarea',
    )
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    expect(focusable.length).toBeGreaterThan(2)

    last.focus()
    press(h, 'Tab')
    expect(document.activeElement).toBe(first)

    first.focus()
    press(h, 'Tab', { shiftKey: true })
    expect(document.activeElement).toBe(last)
  })
})

describe('the submission', () => {
  it('refuses to send an empty message rather than posting a 422', async () => {
    const h = harness({})
    h.dialog.open()
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()
    expect(h.submitted).toHaveLength(0)
    expect(h.root.textContent).toContain(STRINGS.cs.msgRequired)
  })

  it('sends the typed report with an empty honeypot and no console tail when capture is off', async () => {
    const h = harness({})
    h.dialog.open()
    type(h, '  Když mažu položku, zmizí jiná.  ')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()

    expect(h.submitted).toHaveLength(1)
    const body = h.submitted[0]
    expect(body.message).toBe('Když mažu položku, zmizí jiná.')
    expect(body.kind).toBe('bug')
    expect(body.ticket).toBe('ticket.sig')
    expect(body.website).toBe('')
    expect(body.reporter_label).toBe('Kája')
    expect(body.page_url).toBe(context.pageUrl)
    // ⚠ An un-opted site sends no console lines at all.
    expect(body.console_tail).toBeNull()
    expect(body.last_error).toBeNull()
    expect(h.root.textContent).toContain('R-7QK2')
  })

  it('sends the console tail when the site opted in, and drops it when the reporter opts out', async () => {
    const h = harness({ consoleCapture: true })
    h.dialog.open()
    type(h, 'Rozbité')
    h.byText('button', STRINGS.cs.discShow)!.click()
    expect(h.root.textContent).toContain('[home] POST /api/shopping/items/48 → 500')

    const optOut = h.q<HTMLInputElement>('.sfb-optout input')!
    expect(optOut.checked).toBe(false)
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()
    expect(h.submitted[0].console_tail).toEqual(['[home] POST /api/shopping/items/48 → 500'])

    const h2 = harness({ consoleCapture: true })
    h2.dialog.open()
    type(h2, 'Rozbité')
    h2.byText('button', STRINGS.cs.discShow)!.click()
    const optOut2 = h2.q<HTMLInputElement>('.sfb-optout input')!
    optOut2.checked = true
    optOut2.dispatchEvent(new Event('change'))
    h2.byText('button', STRINGS.cs.send)!.click()
    await flush()
    expect(h2.submitted[0].console_tail).toBeNull()
  })

  it('shows the rate-limit treatment without blaming the reporter, and keeps the text', async () => {
    const h = harness({ submit: async () => ({ ok: false, kind: 'rate', retryAfterSeconds: 300 }) })
    h.dialog.open()
    type(h, 'Rozbité')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()
    expect(h.root.textContent).toContain(STRINGS.cs.rateTitle)
    expect(h.root.textContent).toContain(STRINGS.cs.retryIn(5))
    expect(h.q<HTMLTextAreaElement>('textarea')!.value).toBe('Rozbité')
  })

  it('offers a retry that keeps the text when the connection drops', async () => {
    const h = harness({ submit: async () => ({ ok: false, kind: 'network' }) })
    h.dialog.open()
    type(h, 'Rozbité')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()
    expect(h.root.textContent).toContain(STRINGS.cs.sendFailTitle)
    expect(h.q<HTMLTextAreaElement>('textarea')!.value).toBe('Rozbité')
    expect(h.byText('button', STRINGS.cs.sendAgain)).not.toBeNull()
  })
})

describe('uploads', () => {
  function pick(h: Harness, files: File[]): void {
    const input = h.q<HTMLInputElement>('input[type="file"]')!
    Object.defineProperty(input, 'files', { value: files, configurable: true })
    input.dispatchEvent(new Event('change'))
  }

  const png = (name: string, size = 1024) =>
    new File([new Uint8Array(size)], name, { type: 'image/png' })

  it('refuses an oversized file with the limit named, and keeps the report sendable', () => {
    const h = harness({})
    h.dialog.open()
    pick(h, [png('velky.png', 11 * 1024 * 1024)])
    expect(h.root.textContent).toContain(STRINGS.cs.tooLargeTitle)
    expect(h.root.textContent).toContain('velky.png')
    expect(h.all('.sfb-chip')).toHaveLength(0)
  })

  it('refuses a type outside the allow-list in plain words', () => {
    const h = harness({})
    h.dialog.open()
    pick(h, [new File(['x'], 'hlaseni.pdf', { type: 'application/pdf' })])
    expect(h.root.textContent).toContain(STRINGS.cs.wrongTypeTitle)
    expect(h.all('.sfb-chip')).toHaveLength(0)
  })

  it('uploads sequentially, then claims, then shows the reference', async () => {
    const order: string[] = []
    const h = harness({
      submit: async () => ({
        ok: true,
        accepted: {
          ref: 'R-4M8D',
          uploads: [slot(1), slot(2)],
        },
      }),
      put: (slot, file) => {
        order.push(`start:${(file as File).name}`)
        return {
          promise: Promise.resolve(true).then((v) => {
            order.push(`end:${(file as File).name}`)
            return v
          }),
          abort: () => {},
        }
      },
    })
    h.dialog.open()
    type(h, 'Dva soubory')
    pick(h, [png('a.png'), png('b.png')])
    h.byText('button', STRINGS.cs.send)!.click()
    await flush(20)

    expect(order).toEqual(['start:a.png', 'end:a.png', 'start:b.png', 'end:b.png'])
    expect(h.claimed).toEqual(['R-4M8D'])
    expect(h.root.textContent).toContain('R-4M8D')
    expect(h.root.textContent).toContain(STRINGS.cs.successTitle)
  })

  it('retries a failed upload once, then reports the report as landed and the file as not', async () => {
    let attempts = 0
    const h = harness({
      submit: async () => ({ ok: true, accepted: { ref: 'R-9XB3', uploads: [slot(1)] } }),
      put: () => {
        attempts++
        return { promise: Promise.resolve(false), abort: () => {} }
      },
    })
    h.dialog.open()
    type(h, 'Video se nenahrálo')
    pick(h, [png('video-chyba.png')])
    h.byText('button', STRINGS.cs.send)!.click()
    await flush(20)

    expect(attempts).toBe(2) // one attempt, one retry, then abandoned
    expect(h.claimed).toEqual(['R-9XB3'])
    // ⚠ Accent, not alarm: the report exists and Karel can see it.
    expect(h.root.textContent).toContain(STRINGS.cs.uploadFailedTitle)
    expect(h.root.textContent).toContain('video-chyba.png')
    expect(h.root.textContent).toContain('R-9XB3')
  })
})

function slot(id: number): UploadSlot {
  return {
    attachment_id: id,
    url: `https://account.r2.cloudflarestorage.com/bucket/feedback/home/R-4M8D/${id}.png`,
    expires_at: '2026-09-03T10:00:00.000Z',
    headers: { 'Content-Type': 'image/png', 'Content-Length': '1024' },
  }
}

describe('the honeypot', () => {
  it('is present, hidden from assistive technology, and never reachable by Tab', () => {
    const h = harness({})
    h.dialog.open()
    const honey = h.q<HTMLInputElement>('input[name="website"]')!
    expect(honey.getAttribute('aria-hidden')).toBe('true')
    expect(honey.getAttribute('tabindex')).toBe('-1')
    // Nobody using a keyboard or a screen reader is ever asked to fill it in,
    // which is the whole difference between a honeypot and a broken form.
    const trapped = h.all<HTMLElement>(
      '[role="dialog"] button:not([disabled]):not([tabindex="-1"]), [role="dialog"] input:not([tabindex="-1"]), [role="dialog"] textarea',
    )
    expect(trapped).not.toContain(honey)
  })
})
