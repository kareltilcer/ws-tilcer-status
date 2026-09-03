import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { SubmitResult, Upload } from './api'
import type { ConsoleCapture } from './consoleTail'
import { FeedbackDialog, resolveKinds, type DialogApi, type ReportContext } from './dialog'
import { limitsFrom } from './files'
import { STRINGS } from './i18n'
import { WIDGET_CSS } from './styles'
import type { Kind, Submission, UploadSlot } from './types'

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
  kinds?: Kind[]
  consoleCapture?: boolean
  capture?: ConsoleCapture
  submit?: (body: Submission) => Promise<SubmitResult>
  put?: (slot: UploadSlot, file: Blob, onProgress: (p: number) => void) => Upload
  ticket?: string | null
  ticketOwedMs?: () => number
  refreshTicket?: () => Promise<void>
  ticketSettled?: () => Promise<void>
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
    // Old enough by default (nothing owed): every test that is not about the
    // dwell should not have to wait one out.
    ticketOwedMs: () => (opts.ticketOwedMs ? opts.ticketOwedMs() : -60_000),
    refreshTicket: () => (opts.refreshTicket ? opts.refreshTicket() : Promise.resolve()),
    ticketSettled: () => (opts.ticketSettled ? opts.ticketSettled() : Promise.resolve()),
  }

  const dialog = new FeedbackDialog({
    root,
    strings: STRINGS.cs,
    limits,
    kinds: opts.kinds ?? ['bug', 'idea', 'other'],
    consoleCapture: opts.consoleCapture ?? false,
    capture: opts.capture ?? capture,
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

  // ⚠ The dialog element itself is a focus position, not just a container:
  // holdFocus parks focus there after any re-render that removed the focused
  // control. Shift+Tab from there was not intercepted at all — the browser
  // walked backwards out of the dialog, to the launcher and then into the host
  // page, and since the keydown listener is bound to the dialog, Escape went
  // with it.
  it('traps Shift+Tab when focus is parked on the dialog itself', () => {
    const h = harness({})
    h.dialog.open()
    const dialog = h.q<HTMLElement>('[role="dialog"]')!
    const focusable = h.all<HTMLElement>(
      '[role="dialog"] button:not([disabled]):not([tabindex="-1"]), [role="dialog"] textarea',
    )
    dialog.focus()
    expect(document.activeElement).toBe(dialog)
    press(h, 'Tab', { shiftKey: true })
    expect(document.activeElement).toBe(focusable[focusable.length - 1])
  })

  // ⚠ Escape swaps the footer under a reporter whose focus is still in the
  // textarea: nothing moves, so nothing is announced. A screen-reader user hears
  // silence, reads it as "Escape did nothing", and presses it again — and the
  // second press is the one that discards what they wrote.
  it('announces the discard prompt rather than silently swapping the footer', () => {
    const h = harness({})
    h.dialog.open()
    type(h, 'Něco se pokazilo')
    press(h, 'Escape')
    expect(h.byText('[role="alert"]', STRINGS.cs.discardTitle)).not.toBeNull()
  })

  // ⚠ The backdrop and the centring wrapper are ONE element. Two full-viewport
  // fixed layers means the upper one takes every click meant for the lower, so a
  // handler on a separate scrim underneath can never fire. jsdom has no layout
  // and cannot see the stacking, but it can see which element the handler is on,
  // which is the half that was wrong.
  it('closes on a click on the backdrop, and not on a click inside the dialog', () => {
    const h = harness({})
    h.dialog.open()
    h.q<HTMLElement>('[role="dialog"]')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(h.dialog.isOpen).toBe(true)
    h.q<HTMLElement>('.sfb-wrap')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(h.dialog.isOpen).toBe(false)
  })

  // ⚠ Pressing Send clears the footer the Send button is in. Focus would fall to
  // <body> — outside the shadow root, outside the keydown listener — and Escape
  // and the focus trap would both be dead for the rest of the opening.
  it('keeps focus inside the dialog when a phase change removes the focused control', () => {
    const h = harness({})
    h.dialog.open()
    type(h, 'Rozbilo se to')
    const send = h.byText('button', STRINGS.cs.send)!
    send.focus()
    expect(document.activeElement).toBe(send)
    send.click()
    const dialog = h.q<HTMLElement>('[role="dialog"]')!
    expect(document.activeElement).not.toBe(document.body)
    expect(dialog.contains(document.activeElement)).toBe(true)
  })
})

describe('the widget’s typography', () => {
  // ⚠ The host element carries an inline `all:initial` (main.ts), which expands
  // to `font-family: initial; line-height: initial`. An inline style is an
  // element-attached declaration in the OUTER encapsulation context, and for
  // normal rules the outer context wins — so `:host { font-family: var(--sfb-font) }`
  // lost to it and every string in the dialog and the launcher rendered in the
  // UA's default serif at line-height `normal`, on every host page (measured in
  // Chrome: the shadow child computed "Times New Roman"). Nothing outside the
  // shadow root can select what is inside it, so the two inherited declarations
  // have to sit below the host rather than on it.
  it('applies the font stack below the host, where the host page cannot outrank it', () => {
    const host = WIDGET_CSS.match(/:host\s*\{([^}]*)\}/)![1]
    expect(host).not.toMatch(/font-family\s*:/)
    expect(host).not.toMatch(/line-height\s*:/)
    const kids = WIDGET_CSS.match(/:host\s*>\s*\*\s*\{([^}]*)\}/)![1]
    expect(kids).toMatch(/font-family:\s*var\(--sfb-font\)/)
    expect(kids).toMatch(/line-height:/)
  })
})

describe('the kind picker', () => {
  // ⚠ The stylesheet is the only thing that makes the choice VISIBLE — the accent
  // fill and the check glyph both hang off an attribute selector. Round 2 moved
  // the DOM from `aria-checked` to `aria-pressed` and left the CSS behind, which
  // left the picker with no selected state at all: not colour, not glyph. This
  // ties the two together so they cannot drift apart again silently.
  it('styles the selected kind on the attribute the dialog actually sets', () => {
    const h = harness({})
    h.dialog.open()
    const selectors = Array.from(WIDGET_CSS.matchAll(/\.sfb-kind\[([a-z-]+)=/g)).map((m) => m[1])
    expect(selectors.length).toBeGreaterThan(0)
    const first = h.q<HTMLElement>('.sfb-kind')!
    for (const attr of new Set(selectors)) expect(first.getAttribute(attr)).toBe('true')
  })

  // ⚠ Not role="radiogroup". That role promises one tab stop and arrow-key
  // navigation; these are three buttons that say which one is chosen.
  it('says which kind is chosen without claiming to be a radiogroup', () => {
    const h = harness({})
    h.dialog.open()
    expect(h.q('[role="radiogroup"]')).toBeNull()
    expect(h.q('[role="radio"]')).toBeNull()
    const kinds = h.all<HTMLElement>('.sfb-kind')
    expect(kinds.map((k) => k.getAttribute('aria-pressed'))).toEqual(['true', 'false', 'false'])
    kinds[1].click()
    expect(kinds.map((k) => k.getAttribute('aria-pressed'))).toEqual(['false', 'true', 'false'])
  })

  // The server publishes `kinds` so the widget can follow it; offering one the
  // submit endpoint would 422 shows the reporter a generic send failure.
  it('offers what the config published, and falls back when it published nothing', async () => {
    const h = harness({ kinds: resolveKinds(['idea', 'other']) })
    h.dialog.open()
    expect(h.all('.sfb-kind')).toHaveLength(2)
    type(h, 'Nápad')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()
    expect(h.submitted[0].kind).toBe('idea')

    expect(resolveKinds(undefined)).toEqual(['bug', 'idea', 'other'])
    expect(resolveKinds(['nonsense'])).toEqual(['bug', 'idea', 'other'])
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
    // ⚠ No action button inside the alert. One labelled "try again in 5
    // minutes" that submits the instant it is pressed answers with this same
    // alert, forever; the footer's own Send is the retry, when the wait is over.
    expect(h.all('.sfb-alert button')).toHaveLength(0)
    expect(h.byText('button', STRINGS.cs.send)).not.toBeNull()
  })

  // ⚠ `isOpen` is true again after a close-and-reopen, so a submission the
  // reporter walked away from used to resume into the dialog they had since
  // opened — clearing the body and replacing what they were typing with the
  // previous report's success screen.
  it('never lets an abandoned submission reach into the next opening', async () => {
    let release: (r: SubmitResult) => void = () => {}
    const h = harness({ submit: () => new Promise<SubmitResult>((resolve) => { release = resolve }) })
    h.dialog.open()
    type(h, 'První hlášení')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()

    // Impatient on a slow link: close, then come back and start again.
    press(h, 'Escape')
    expect(h.dialog.isOpen).toBe(false)
    h.dialog.open()
    type(h, 'Druhé hlášení')

    release({ ok: true, accepted: { ref: 'R-7QK2', uploads: [] } })
    await flush()
    expect(h.q<HTMLTextAreaElement>('textarea')?.value).toBe('Druhé hlášení')
    expect(h.root.textContent).not.toContain('R-7QK2')
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

  // ⚠ The retry has to post a ticket the server will accept. The replacement is
  // minted when the failure is SHOWN, and the retry waits out whatever dwell it
  // still owes — a ticket minted at the moment of the click is younger than
  // STATUS_FEEDBACK_MIN_DWELL_MS and is refused as a script, every time, which
  // made "Odeslat znovu" a button that could never work.
  //
  // ⚠ The dwell here is 10 s, deliberately longer than the 3 500 ms the widget
  // used to hardcode as a mirror of the server's default. STATUS_FEEDBACK_MIN_DWELL_MS
  // is a dial a deployment may set to anything under 30 s, and against the fixed
  // constant every retry on such a deployment posted a ticket the server refused
  // — "Send again" that could never work, forever. The wait now comes from
  // `min_dwell_ms` in the config, through `ticketOwedMs`.
  it('waits out a configured dwell longer than the old hardcoded one', async () => {
    vi.useFakeTimers()
    try {
      const dwell = 10_000
      let issuedAt = Date.now()
      let refreshes = 0
      const h = harness({
        submit: async () => ({ ok: false, kind: 'network' }),
        ticketOwedMs: () => dwell - (Date.now() - issuedAt),
        refreshTicket: async () => {
          refreshes++
          issuedAt = Date.now()
        },
      })
      h.dialog.open()
      type(h, 'Rozbité')
      h.byText('button', STRINGS.cs.send)!.click()
      await flush()
      // The replacement is minted with the alert, not with the click on it.
      expect(refreshes).toBe(1)

      h.byText('button', STRINGS.cs.sendAgain)!.click()
      await flush()
      expect(h.submitted).toHaveLength(1) // still waiting out the dwell
      expect(h.root.textContent).toContain(STRINGS.cs.sending)

      // Past the old constant, nowhere near this deployment's dwell.
      await vi.advanceTimersByTimeAsync(4_000)
      await flush()
      expect(h.submitted).toHaveLength(1)

      await vi.advanceTimersByTimeAsync(7_000)
      await flush()
      expect(h.submitted).toHaveLength(2)
    } finally {
      vi.useRealTimers()
    }
  })

  // ⚠ The retry's dwell READS as "Odesílám…" but nothing is in flight and
  // nothing is stored: the POST has not happened yet. Closing on the phase alone
  // therefore threw away a report that only existed in the textarea, without the
  // question every other route to losing it asks first.
  it('asks before discarding while the retry is only waiting out the dwell', async () => {
    vi.useFakeTimers()
    try {
      let issuedAt = Date.now()
      const h = harness({
        submit: async () => ({ ok: false, kind: 'network' }),
        ticketOwedMs: () => 3_500 - (Date.now() - issuedAt),
        refreshTicket: async () => {
          issuedAt = Date.now()
        },
      })
      h.dialog.open()
      type(h, 'Rozbité')
      h.byText('button', STRINGS.cs.send)!.click()
      await flush()
      h.byText('button', STRINGS.cs.sendAgain)!.click()
      await flush()
      expect(h.submitted).toHaveLength(1) // still waiting; nothing has been sent

      press(h, 'Escape')
      expect(h.dialog.isOpen).toBe(true)
      expect(h.root.textContent).toContain(STRINGS.cs.discardTitle)

      // ⚠ And the dwell runs OUT while the question stands. The timer that
      // resumes the retry used to walk straight past the prompt, clear it and
      // post — the widget answering its own modal question, which is the same
      // failure as discarding without asking seen from the other side: the
      // reporter asked to throw the report away and it was filed instead.
      await vi.advanceTimersByTimeAsync(5_000)
      expect(h.submitted).toHaveLength(1)
      expect(h.root.textContent).toContain(STRINGS.cs.discardTitle)

      // Keeping it puts the reporter back on a form they can send themselves.
      h.byText('button', STRINGS.cs.keepEditing)!.click()
      expect(h.root.textContent).not.toContain(STRINGS.cs.discardTitle)
      h.byText('button', STRINGS.cs.send)!.click()
      await flush()
      expect(h.submitted).toHaveLength(2)
    } finally {
      vi.useRealTimers()
    }
  })

  // ⚠ The SAME hazard, one await earlier, and the likelier of the two: before
  // the retry can wait out a dwell it has to wait for the replacement ticket to
  // arrive, and the send failed because the network is down — so that fetch runs
  // the full 10-second config timeout. The phase says "Odesílám…" for all of it
  // while nothing has been posted, and until `dwelling` covered this await too a
  // ✕ during those ten seconds threw the typed report away without asking.
  it('asks before discarding while the retry is still waiting for a ticket', async () => {
    let settle = (): void => {}
    const held = new Promise<void>((resolve) => {
      settle = resolve
    })
    let settled = 0
    const h = harness({
      submit: async () => ({ ok: false, kind: 'network' }),
      // The first send's own wait resolves; the retry's is the one held open.
      ticketSettled: () => (++settled > 1 ? held : Promise.resolve()),
    })
    h.dialog.open()
    type(h, 'Rozbité')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()

    h.byText('button', STRINGS.cs.sendAgain)!.click()
    await flush()
    expect(h.submitted).toHaveLength(1) // nothing posted; still waiting for the ticket
    expect(h.root.textContent).toContain(STRINGS.cs.sending)

    press(h, 'Escape')
    expect(h.dialog.isOpen).toBe(true)
    expect(h.root.textContent).toContain(STRINGS.cs.discardTitle)

    // ⚠ And the ticket ARRIVES while the question stands. The resume used to
    // clear the prompt and post the report the reporter had just asked to
    // discard, on the likelier of the two waits.
    settle()
    await flush()
    expect(h.submitted).toHaveLength(1)
    expect(h.root.textContent).toContain(STRINGS.cs.discardTitle)
    expect(h.dialog.isOpen).toBe(true)
  })

  // ⚠ The alert's "Odeslat znovu" lives in the BODY and the discard question in
  // the FOOTER, so both are on screen at once — and a reporter who pressed ✕ and
  // then decided to retry has answered the question by retrying. Left standing,
  // it rendered over the sending phase, the uploads and the success screen, with
  // a Discard button that closed the dialog mid-upload.
  it('clears a standing discard prompt when the reporter retries instead of answering it', async () => {
    let attempt = 0
    const h = harness({
      submit: async () =>
        ++attempt === 1
          ? { ok: false, kind: 'network' }
          : { ok: true, accepted: { ref: 'R-7QK2', uploads: [] } },
    })
    h.dialog.open()
    type(h, 'Rozbité')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()

    press(h, 'Escape')
    expect(h.root.textContent).toContain(STRINGS.cs.discardTitle)

    h.byText('button', STRINGS.cs.sendAgain)!.click()
    await flush()
    expect(h.submitted).toHaveLength(2)
    expect(h.dialog.isOpen).toBe(true)
    expect(h.root.textContent).toContain(STRINGS.cs.successTitle)
    expect(h.root.textContent).not.toContain(STRINGS.cs.discardTitle)
  })

  // ⚠ `dwelling` is one flag shared by every opening, and a close does not
  // unwind the chain that set it — the abandoned `setTimeout` still resumes. If
  // it cleared the flag on its way out it would clear a LATER opening's dwell,
  // reopening the silent discard the flag exists to close. The abandoned chain
  // therefore clears nothing and `reset()` does it on close.
  it('does not let an abandoned opening clear a later one’s dwell', async () => {
    vi.useFakeTimers()
    try {
      let issuedAt = Date.now()
      const h = harness({
        submit: async () => ({ ok: false, kind: 'network' }),
        ticketOwedMs: () => 3_500 - (Date.now() - issuedAt),
        refreshTicket: async () => {
          issuedAt = Date.now()
        },
      })
      // Opening 1: into the dwell, then walked away from.
      h.dialog.open()
      type(h, 'Rozbité')
      h.byText('button', STRINGS.cs.send)!.click()
      await flush()
      h.byText('button', STRINGS.cs.sendAgain)!.click()
      await flush()
      await vi.advanceTimersByTimeAsync(200)
      press(h, 'Escape')
      h.byText('button', STRINGS.cs.discard)!.click()
      expect(h.dialog.isOpen).toBe(false)

      // Opening 2, starting late enough that opening 1's timer fires mid-dwell.
      h.dialog.open()
      type(h, 'Zase rozbité')
      h.byText('button', STRINGS.cs.send)!.click()
      await flush()
      h.byText('button', STRINGS.cs.sendAgain)!.click()
      await flush()
      await vi.advanceTimersByTimeAsync(3_400) // past opening 1's 3 500 ms mark
      expect(h.submitted).toHaveLength(2) // opening 2 is still dwelling

      press(h, 'Escape')
      expect(h.dialog.isOpen).toBe(true)
      expect(h.root.textContent).toContain(STRINGS.cs.discardTitle)
    } finally {
      vi.useRealTimers()
    }
  })

  // A 413 is the one send failure the reporter can fix, so it must not be
  // dressed up as a dropped connection they can only retry into.
  it('names the length when the body is over the cap, instead of blaming the connection', async () => {
    const h = harness({ submit: async () => ({ ok: false, kind: 'tooLarge' }) })
    h.dialog.open()
    type(h, 'Dlouhé hlášení')
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()
    expect(h.root.textContent).toContain(STRINGS.cs.tooLongTitle)
    expect(h.root.textContent).not.toContain(STRINGS.cs.sendFailBody)
    expect(h.q<HTMLTextAreaElement>('textarea')!.value).toBe('Dlouhé hlášení')
  })

  // The tail is the only part of the body the widget generates rather than
  // receives, so it is the part that gives way to a long report.
  it('trims the console tail to what the rest of the payload leaves of the body cap', async () => {
    const noisy = Array.from({ length: 50 }, (_, i) => `[home] line ${i} ${'x'.repeat(180)}`)
    const h = harness({
      consoleCapture: true,
      capture: { lines: () => noisy, lastError: () => null },
    })
    h.dialog.open()
    type(h, 'ě'.repeat(2000)) // 4 000 bytes of Czech on its own
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()

    const body = h.submitted[0]
    expect(body.console_tail!.length).toBeLessThan(noisy.length)
    expect(new TextEncoder().encode(JSON.stringify(body)).length).toBeLessThanOrEqual(8192)
    // What survives is the NEWEST end — the lines nearest the failure.
    expect(body.console_tail![body.console_tail!.length - 1]).toBe(noisy[noisy.length - 1])
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

  // ⚠ One picker dialog, three files, the bad one in the middle. The rejection
  // used to end the loop, so the screenshot chosen AFTER the oversized clip was
  // dropped with no chip and no second alert — the reporter was told about the
  // clip and quietly lost a file they had picked. One alert is still right; one
  // alert is not a reason to abandon the rest of the selection.
  it('keeps the valid files picked after a rejected one, and still shows one alert', () => {
    const h = harness({})
    h.dialog.open()
    pick(h, [png('a.png'), png('velky.png', 11 * 1024 * 1024), png('b.png')])
    expect(h.all('.sfb-chip')).toHaveLength(2)
    expect(h.root.textContent).toContain('a.png')
    expect(h.root.textContent).toContain('b.png')
    expect(h.all('.sfb-alert')).toHaveLength(1)
    expect(h.root.textContent).toContain(STRINGS.cs.tooLargeTitle)
  })

  // Telling someone whose file reads as 0 bytes to "attach an image instead"
  // names the one thing they did do.
  it('refuses an empty file as empty, not as the wrong type', () => {
    const h = harness({})
    h.dialog.open()
    pick(h, [png('prazdny.png', 0)])
    expect(h.root.textContent).toContain(STRINGS.cs.emptyFileTitle)
    expect(h.root.textContent).not.toContain(STRINGS.cs.wrongTypeBody)
    expect(h.all('.sfb-chip')).toHaveLength(0)
  })

  // ⚠ The server signs one slot per file DECLARED, each for an exact content
  // type and byte length, and the upload pairs them by index — so the list has
  // to stop being editable the moment Send is pressed.
  it('freezes the attachment list while the report is in flight', async () => {
    let release: (r: SubmitResult) => void = () => {}
    const h = harness({
      submit: () => new Promise<SubmitResult>((resolve) => { release = resolve }),
    })
    h.dialog.open()
    type(h, 'Dva soubory')
    pick(h, [png('a.png'), png('b.png')])
    h.byText('button', STRINGS.cs.send)!.click()
    await flush()

    const remove = h.all<HTMLButtonElement>('.sfb-chip .sfb-iconbtn')
    expect(remove).toHaveLength(2)
    expect(remove.every((b) => b.disabled)).toBe(true)
    remove[0].click()
    expect(h.all('.sfb-chip')).toHaveLength(2)
    expect(h.submitted[0].files).toHaveLength(2)

    release({ ok: true, accepted: { ref: 'R-5TT1', uploads: [] } })
    await flush(20)
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
