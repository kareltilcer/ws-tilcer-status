// The dialog: form → uploading → success, plus the six failure treatments from
// HANDOFF-design-v3.md §4.5.
//
// It is deliberately independent of how it is mounted. `main.ts` gives it a
// closed shadow root; a test gives it a plain element. That is what makes the
// focus trap and the submit flow testable at all — a closed root is, by
// definition, unreadable from outside.

import type { ConsoleCapture } from './consoleTail'
import { trimToBudget, utf8Length } from './consoleTail'
import { clear, el, icon } from './dom'
import type { Limits } from './files'
import { isVideo, normalizeType, validateFile } from './files'
import { formatBytes } from './format'
import type { Strings } from './i18n'
import type { SubmitResult, Upload } from './api'
import type { Kind, Submission, UploadSlot } from './types'

const MAX_MESSAGE_CHARS = 4000

/** BODY_SLACK covers the difference between our byte count of the payload and
 *  the bytes the runtime's own JSON.stringify puts on the wire (escapes it
 *  chooses, the ticket's length, a header or two of rounding). */
const BODY_SLACK = 256

/** ReportContext is everything the widget attaches without being asked. It is
 *  also exactly what the disclosure block shows, because a person who can see
 *  what they are attaching can decline to attach it (PRD §V3-8). */
export interface ReportContext {
  pageUrl: string | null
  referrer: string | null
  viewport: string | null
  locale: string | null
  appRelease: string | null
  reporterLabel: string | null
  /** browserLabel is display-only. The user agent reaches the report as the
   *  request header the server reads, not as a field — but it IS sent, so the
   *  disclosure names it. */
  browserLabel: string
}

export interface DialogApi {
  submit(body: Submission): Promise<SubmitResult>
  put(slot: UploadSlot, file: Blob, onProgress: (pct: number) => void): Upload
  claim(ref: string): Promise<void>
  /** ticket returns the current unspent submission ticket, or null. */
  ticket(): string | null
  /** ticketOwedMs is how much longer the held ticket must age before the server
   *  will accept it — zero or negative once it is old enough.
   *
   *  ⚠ The dwell it is measured against is `min_dwell_ms` from the config, not a
   *  constant here. This dialog does not read the config, so the one place that
   *  does (`main.ts`) owes the answer, and a deployment that raises the dial
   *  cannot leave a send waiting a fixed 3.5 s for a ticket the server will
   *  refuse anyway. */
  ticketOwedMs(): number
  /** refreshTicket fetches a new one. Its failure is silent — and leaves the
   *  ticket it already holds in place. */
  refreshTicket(): Promise<void>
  /** ticketSettled resolves once any in-flight refresh has landed. */
  ticketSettled(): Promise<void>
}

/** ALL_KINDS is the order the strings are written in — `s.kinds[i]` is indexed
 *  against it, so nothing else may reorder it. */
const ALL_KINDS: Kind[] = ['bug', 'idea', 'other']

/**
 * resolveKinds picks the kinds to offer from the config the server published.
 *
 * ⚠ The server publishes `kinds` so that the widget FOLLOWS it. Offering a kind
 * the submit endpoint no longer accepts turns a 422 into "sending did not work",
 * which names neither the cause nor a way out. Anything the bundle has no label
 * for is dropped rather than shown untranslated, and an answer with nothing left
 * falls back to all three — an empty picker would be worse than a stale one.
 */
export function resolveKinds(kinds: readonly string[] | undefined): Kind[] {
  const known = (kinds ?? []).filter((k): k is Kind => (ALL_KINDS as string[]).includes(k))
  return known.length > 0 ? known : ALL_KINDS.slice()
}

export interface DialogOptions {
  root: ParentNode
  strings: Strings
  limits: Limits
  /** kinds is what the config endpoint published, resolved by resolveKinds. */
  kinds: Kind[]
  consoleCapture: boolean
  capture: ConsoleCapture
  context: () => ReportContext
  api: DialogApi
  /** onClosed hands focus back to whatever opened the dialog. */
  onClosed: () => void
}

/**
 * Phase is the whole of the dialog's progress through a report, and `presend` is
 * the reason it is spelled out rather than carried in flags beside it.
 *
 * ⚠ `presend` is the window between pressing Send and the body going out: the
 * wait on a replacement ticket, and the wait on that ticket becoming old enough
 * for the server to accept it. It READS as sending — the footer says so — but
 * nothing has been posted and nothing is stored, so closing has to ask exactly
 * as it does on the form. It was a `dwelling` boolean beside the phase for three
 * review rounds, and each round found the next way for that flag to outlive, or
 * be cleared by, the send it belonged to. A phase cannot: every transition that
 * ends a send names the state it ends in, so there is nothing left to clear.
 */
type Phase = 'form' | 'presend' | 'sending' | 'uploading' | 'done'
type FileState = 'waiting' | 'uploading' | 'done' | 'failed'

interface Attachment {
  file: File
  /** type is the file's content type, normalized once at the picker — the value
   *  validated, declared to the server, and used to choose the icon. */
  type: string
  state: FileState
  pct: number
}

/** fileMark and fileText are the two halves the chip list and the upload list
 *  draw identically. The mark is returned rather than inlined because the upload
 *  rows recolour it as the state changes. */
function fileMark(a: Attachment): HTMLElement {
  return el('span', {
    attrs: { style: 'flex:none;color:var(--sfb-muted)' },
    kids: [icon(isVideo(a.type) ? 'video' : 'image', 17)],
  })
}

function fileText(a: Attachment, s: Strings): HTMLElement {
  return el('div', {
    attrs: { style: 'flex:1;min-width:0' },
    kids: [
      el('div', { cls: 'sfb-chip-name', text: a.file.name }),
      el('div', { cls: 'sfb-chip-size', text: formatBytes(a.file.size, s.lang) }),
    ],
  })
}

export class FeedbackDialog {
  private o: DialogOptions
  /** wrap is the backdrop and the centring wrapper at once — see styles.ts. */
  private wrap: HTMLElement | null = null
  private dialog: HTMLElement | null = null
  private body: HTMLElement | null = null
  private foot: HTMLElement | null = null

  private phase: Phase = 'form'
  private message = ''
  private kind: Kind = 'bug'
  private attachments: Attachment[] = []
  private discOpen = false
  private consoleOptOut = false
  private discardPrompt = false
  private ref: string | null = null
  /**
   * generation counts openings. Every await in the submit chain resumes against
   * it, not against `isOpen`.
   *
   * ⚠ `isOpen` is true again after a close-and-reopen, so guarding on it lets a
   * submission the reporter walked away from resume into the dialog they have
   * since opened: `renderSuccess` would clear the body and throw away the report
   * they were half-way through typing, under the PREVIOUS report's reference.
   * A number that only ever goes up cannot be confused that way.
   */
  private generation = 0
  private activeUpload: Upload | null = null
  private disclosureHost: HTMLElement | null = null
  private uploadStatus: HTMLElement | null = null
  private uploadRows: { label: HTMLElement; bar: HTMLElement; fill: HTMLElement; mark: HTMLElement }[] = []

  // Live nodes the update path writes to, rather than re-rendering the form and
  // losing the caret in the middle of a sentence.
  private msgInput: HTMLTextAreaElement | null = null
  private counter: HTMLElement | null = null
  private filesEl: HTMLElement | null = null
  private pickBtn: HTMLButtonElement | null = null
  private fileInput: HTMLInputElement | null = null
  private attachHintEl: HTMLElement | null = null
  private alertHost: HTMLElement | null = null
  private discBody: HTMLElement | null = null
  private discToggle: HTMLButtonElement | null = null
  private honeypot: HTMLInputElement | null = null

  constructor(options: DialogOptions) {
    this.o = options
    this.kind = options.kinds[0]
  }

  get isOpen(): boolean {
    return this.wrap !== null
  }

  open(): void {
    if (this.isOpen) return
    this.generation++
    this.reset()
    this.mount()
    this.msgInput?.focus()
  }

  /** live answers whether the opening a continuation belongs to is still the one
   *  on screen. Every resume point in the submit chain goes through it. */
  private live(generation: number): boolean {
    return this.isOpen && generation === this.generation
  }

  close(): void {
    // ⚠ Closing mid-upload abandons the remaining files rather than trapping the
    // reporter in a dialog. The report itself is already stored — the text is the
    // thing worth keeping — and an abandoned object is collected by the nightly
    // sweep, which is what it exists for.
    this.activeUpload?.abort()
    this.activeUpload = null
    this.wrap?.remove()
    // ⚠ `reset()` here, not only at the next open. It is what drops
    // `attachments` — and with them the reporter's Files, up to three 50 MB
    // clips held for as long as the host page lives, on a widget most people
    // close once and never reopen. The four structural nodes go with it, which
    // is also what makes the stale-node guards (`if (!this.body || !this.foot)`)
    // able to fire at all.
    //
    // ⚠ Knowingly not exhaustive: `msgInput` and the other live-node fields
    // still point into the detached tree, and a detached node holds its parent,
    // so one tree survives until `mount()` replaces every one of them on the
    // next open. Nulling all twelve was measured at 48 gzipped bytes — over half
    // of what this bundle has left under §V3-8's 15 kB — to release a few dozen
    // detached nodes that cannot accumulate. Not a trade worth making blind.
    this.wrap = this.dialog = this.body = this.foot = null
    this.uploadRows = []
    this.reset()
    this.o.onClosed()
  }

  /** requestClose is what Escape, the ✕ and the backdrop go through: it asks
   *  before discarding text the reporter typed, because losing that is the real
   *  failure. */
  private requestClose(): void {
    // ⚠ `presend` is in here as well as `form`. During a real send the report is
    // already on its way and closing loses nothing that matters; before the POST
    // it is not — neither while a replacement ticket is being fetched nor while
    // its dwell runs out — so an impatient ✕ on a phase that merely READS as
    // sending would throw the typed report away without ever asking.
    if ((this.phase === 'form' || this.phase === 'presend') && this.dirty() && !this.discardPrompt) {
      this.discardPrompt = true
      this.renderFooter()
      return
    }
    this.close()
  }

  private dirty(): boolean {
    return this.message.trim().length > 0 || this.attachments.length > 0
  }

  private reset(): void {
    this.phase = 'form'
    this.message = ''
    // The first kind the server offers, not a hardcoded 'bug': a config that
    // narrowed the list would otherwise pre-select a kind it will 422.
    this.kind = this.o.kinds[0]
    this.attachments = []
    this.discOpen = false
    this.consoleOptOut = false
    this.discardPrompt = false
    this.ref = null
  }

  // --- structure ------------------------------------------------------------

  private mount(): void {
    const s = this.o.strings
    const titleId = 'sfb-title'

    const head = el('div', {
      cls: 'sfb-head',
      kids: [
        el('span', { cls: 'sfb-head-mark', kids: [icon('bubble', 18)] }),
        el('div', {
          attrs: { style: 'flex:1;min-width:0' },
          kids: [
            el('div', { cls: 'sfb-title', text: s.title, attrs: { id: titleId } }),
            el('div', { cls: 'sfb-sub', text: s.msgHint }),
          ],
        }),
        el('button', {
          cls: 'sfb-iconbtn',
          attrs: { type: 'button', 'aria-label': s.close },
          kids: [icon('x', 20)],
          on: { click: () => this.requestClose() },
        }),
      ],
    })

    this.body = el('div', { cls: 'sfb-body' })
    this.foot = el('div', { cls: 'sfb-foot' })
    this.dialog = el('div', {
      cls: 'sfb-dialog',
      // ⚠ tabindex="-1" is not decoration. Every phase change clears the body or
      // the footer, which can remove the element that had focus; without a
      // programmatic home inside the dialog, focus falls to <body> — outside the
      // shadow root and outside the keydown listener below — and Escape and the
      // focus trap both stop working for the rest of the opening. See holdFocus.
      attrs: { role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': titleId, tabindex: '-1' },
      kids: [head, this.body, this.foot],
      on: { keydown: (e) => this.onKeyDown(e as KeyboardEvent) },
    })
    // The wrap is the backdrop too, so this handler IS click-outside-to-close: it
    // fires only when the click landed on the backdrop and not on the dialog.
    //
    // ⚠ The other branch matters as much. A click on the dialog's own chrome —
    // the title, a hint line, the padding — focuses nothing, so the browser
    // clears focus to <body>; from there the keydown listener on the dialog never
    // fires again and Escape stops closing. Every click inside puts focus back.
    this.wrap = el('div', {
      cls: 'sfb-wrap',
      kids: [this.dialog],
      on: { click: (e) => (e.target === this.wrap ? this.requestClose() : this.holdFocus()) },
    })

    this.o.root.appendChild(this.wrap)
    this.renderForm()
    this.renderFooter()
  }

  /** holdFocus pulls focus back into the dialog after a re-render that removed
   *  the control holding it. It is a no-op whenever focus is still inside. */
  private holdFocus(): void {
    const d = this.dialog
    if (!d || !this.isOpen) return
    const a = this.activeElementIn(d)
    if (!a || !d.contains(a)) d.focus()
  }

  private onKeyDown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.stopPropagation()
      this.requestClose()
      return
    }
    if (e.key !== 'Tab' || !this.dialog) return
    // Focus trap: Tab cycles inside the dialog only, in both directions.
    //
    // ⚠ Visibility is decided by the selector, never by `offsetParent`: the whole
    // dialog is inside a position:fixed wrapper, and a fixed element's
    // offsetParent is null in every browser — filtering on it would empty this
    // list and let Tab escape into the host page.
    const focusable = Array.from(
      this.dialog.querySelectorAll<HTMLElement>(
        'button:not([disabled]):not([tabindex="-1"]), a[href], input:not([disabled]):not([tabindex="-1"]), textarea:not([disabled]), select:not([disabled])',
      ),
    )
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    const active = this.activeElementIn(this.dialog)
    // ⚠ `this.dialog` counts as "before the first": holdFocus parks focus on the
    // dialog itself after any re-render that removed the focused control, which
    // is the ordinary case for pressing Send. Without it here, Shift+Tab from
    // there is not intercepted at all — the browser walks backwards out of the
    // dialog to the launcher and then into the host page, and since the keydown
    // listener is bound to the dialog, Escape stops working too.
    if (e.shiftKey && (active === first || active === this.dialog || active === null)) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && active === last) {
      e.preventDefault()
      first.focus()
    }
  }

  /** activeElementIn resolves the focused node through the shadow boundary — in a
   *  shadow root `document.activeElement` is the host element, not the button. */
  private activeElementIn(scope: HTMLElement): Element | null {
    const root = scope.getRootNode() as Document | ShadowRoot
    return (root as ShadowRoot).activeElement ?? document.activeElement
  }

  // --- the form -------------------------------------------------------------

  private renderForm(): void {
    if (!this.body) return
    const s = this.o.strings
    clear(this.body)

    this.msgInput = el('textarea', {
      cls: 'sfb-textarea',
      attrs: {
        rows: '4',
        maxlength: String(MAX_MESSAGE_CHARS),
        placeholder: s.msgPlaceholder,
        'aria-describedby': 'sfb-msg-hint',
      },
      on: {
        input: () => {
          this.message = this.msgInput?.value ?? ''
          if (this.counter) this.counter.textContent = `${this.message.length} / ${MAX_MESSAGE_CHARS}`
        },
      },
    }) as HTMLTextAreaElement
    this.msgInput.value = this.message
    this.counter = el('span', { cls: 'sfb-count', text: `${this.message.length} / ${MAX_MESSAGE_CHARS}` })

    const messageBlock = el('div', {
      kids: [
        el('label', { cls: 'sfb-label', text: s.msgLabel, attrs: { for: 'sfb-message' } }),
        this.msgInput,
        el('div', {
          cls: 'sfb-meta',
          kids: [el('span', { cls: 'sfb-hint', text: s.msgHint, attrs: { id: 'sfb-msg-hint' } }), this.counter],
        }),
      ],
    })
    this.msgInput.id = 'sfb-message'

    // ⚠ The message comes before the kind (design §11 q5): the person is annoyed
    // and should be typing within a second of the dialog opening. Classification
    // is Karel's convenience and can wait for the sentence to be finished.
    // ⚠ A group of buttons with aria-pressed, NOT role="radiogroup". That role
    // promises a screen-reader user one tab stop and arrow-key navigation
    // between the options; three separate tab stops that ignore ArrowRight are
    // worse than plain buttons, because the role is what said otherwise. The
    // pressed state carries the same "this one is chosen" without the promise,
    // and it is the pattern the dashboard's own choice rows use.
    const kindsRow = el('div', { cls: 'sfb-kinds', attrs: { role: 'group', 'aria-label': s.kindLabel } })
    this.o.kinds.forEach((k) => {
      const btn = el('button', {
        cls: 'sfb-kind',
        attrs: { type: 'button', 'aria-pressed': String(k === this.kind) },
        kids: [icon('check', 14), el('span', { text: s.kinds[ALL_KINDS.indexOf(k)] })],
        on: {
          click: () => {
            this.kind = k
            for (const node of Array.from(kindsRow.children)) {
              node.setAttribute('aria-pressed', String(node === btn))
            }
          },
        },
      })
      kindsRow.appendChild(btn)
    })
    const kindBlock = el('div', {
      kids: [el('div', { cls: 'sfb-label', text: s.kindLabel }), kindsRow],
    })

    this.fileInput = el('input', {
      cls: 'sfb-sr',
      attrs: {
        type: 'file',
        multiple: 'multiple',
        accept: this.o.limits.accept.join(','),
        tabindex: '-1',
        'aria-hidden': 'true',
      },
      on: { change: () => this.onFilesPicked() },
    }) as HTMLInputElement
    this.filesEl = el('div', { cls: 'sfb-files' })
    this.pickBtn = el('button', {
      cls: 'sfb-pick',
      attrs: { type: 'button' },
      kids: [icon('clip', 17), el('span', { text: s.attach })],
      on: { click: () => this.fileInput?.click() },
    }) as HTMLButtonElement
    this.attachHintEl = el('div', { cls: 'sfb-hint' })
    const attachBlock = el('div', {
      kids: [
        el('div', {
          attrs: { style: 'display:flex;flex-direction:column;gap:8px' },
          kids: [this.filesEl, this.pickBtn, this.attachHintEl, this.fileInput],
        }),
      ],
    })

    this.alertHost = el('div')
    this.honeypot = el('input', {
      cls: 'sfb-honey',
      attrs: { type: 'text', name: 'website', tabindex: '-1', autocomplete: 'off', 'aria-hidden': 'true' },
    }) as HTMLInputElement

    this.body.appendChild(this.alertHost)
    this.body.appendChild(messageBlock)
    this.body.appendChild(kindBlock)
    this.body.appendChild(attachBlock)
    this.body.appendChild(this.buildDisclosure())
    this.body.appendChild(this.honeypot)
    this.syncFiles()
  }

  private buildDisclosure(): HTMLElement {
    const s = this.o.strings
    const summary = this.o.consoleCapture ? s.discSummaryConsole : s.discSummary
    this.discBody = el('div', { cls: 'sfb-disclosure-body' })
    this.discToggle = el('button', {
      cls: 'sfb-link',
      attrs: { type: 'button', 'aria-expanded': 'false' },
      kids: [el('span', { text: s.discShow }), icon('chevD', 14)],
      on: { click: () => this.toggleDisclosure() },
    }) as HTMLButtonElement

    const block = el('div', {
      cls: 'sfb-disclosure',
      kids: [
        el('div', {
          cls: 'sfb-disclosure-head',
          kids: [
            el('span', { attrs: { style: 'flex:none;margin-top:2px;color:var(--sfb-muted)' }, kids: [icon('info', 16)] }),
            el('div', {
              attrs: { style: 'flex:1;min-width:0' },
              kids: [
                el('div', {
                  attrs: { style: 'font-size:12.5px' },
                  kids: [el('b', { text: `${s.discLead} ` }), el('span', { text: summary })],
                }),
                this.discToggle,
              ],
            }),
          ],
        }),
      ],
    })
    // The expanded body is attached and detached rather than hidden, so a screen
    // reader never reaches lines the reporter has not asked to see.
    this.disclosureHost = block
    return block
  }

  private toggleDisclosure(): void {
    const s = this.o.strings
    this.discOpen = !this.discOpen
    this.discToggle?.setAttribute('aria-expanded', String(this.discOpen))
    if (this.discToggle) {
      clear(this.discToggle)
      this.discToggle.appendChild(el('span', { text: this.discOpen ? s.discHide : s.discShow }))
      this.discToggle.appendChild(icon(this.discOpen ? 'chevU' : 'chevD', 14))
    }
    if (!this.disclosureHost || !this.discBody) return
    if (!this.discOpen) {
      this.discBody.remove()
      return
    }
    this.fillDisclosure()
    this.disclosureHost.appendChild(this.discBody)
  }

  private fillDisclosure(): void {
    if (!this.discBody) return
    const s = this.o.strings
    const ctx = this.o.context()
    clear(this.discBody)
    const rows: [string, string | null][] = [
      [s.discKeys.pageUrl, ctx.pageUrl],
      [s.discKeys.referrer, ctx.referrer],
      [s.discKeys.browser, ctx.browserLabel],
      [s.discKeys.viewport, ctx.viewport],
      [s.discKeys.locale, ctx.locale],
      [s.discKeys.release, ctx.appRelease],
    ]
    const list = el('div', { attrs: { style: 'display:flex;flex-direction:column;gap:7px' } })
    for (const [k, v] of rows) {
      if (!v) continue
      list.appendChild(el('div', { cls: 'sfb-kv', kids: [el('span', { text: k }), el('span', { text: v })] }))
    }
    this.discBody.appendChild(list)

    if (!this.o.consoleCapture) return
    // ⚠ These are the lines within the tail's OWN cap (3 kB). `fitConsoleTail`
    // may drop more of the oldest at send time, because the budget it trims
    // against is whatever the rest of the payload leaves — so on a long report
    // this block can show a few lines more than actually travel. It is left that
    // way deliberately: the budget is a function of a message the reporter goes
    // on editing after opening this block, so any "exact" view is stale the
    // moment they type another word, and the error is only ever in the safe
    // direction (shown ⊇ sent — the disclosure never under-states what leaves
    // the browser, which is what PRD §V3-8 is protecting).
    const lines = this.o.capture.lines()
    const linesEl = el('div', { cls: 'sfb-console-lines' })
    for (const line of lines) linesEl.appendChild(el('div', { text: line }))
    if (lines.length === 0) linesEl.appendChild(el('div', { text: '—' }))

    const optOut = el('input', {
      attrs: { type: 'checkbox' },
      on: {
        change: (e) => {
          this.consoleOptOut = (e.target as HTMLInputElement).checked
        },
      },
    }) as HTMLInputElement
    optOut.checked = this.consoleOptOut

    this.discBody.appendChild(
      el('div', {
        cls: 'sfb-console',
        kids: [
          el('div', {
            cls: 'sfb-console-head',
            kids: [
              el('div', { attrs: { style: 'font-size:12px;font-weight:600' }, text: s.consoleLead }),
              el('div', { cls: 'sfb-hint', text: s.consoleNote }),
            ],
          }),
          linesEl,
          el('label', { cls: 'sfb-optout', kids: [optOut, el('span', { text: s.consoleOptOut })] }),
        ],
      }),
    )
  }

  private onFilesPicked(): void {
    const input = this.fileInput
    if (!input || !input.files) return
    // The list is frozen once Send is pressed: the server signs one slot per
    // file DECLARED, and the upload pairs them by index.
    if (this.phase !== 'form') return
    const s = this.o.strings
    const picked = Array.from(input.files)
    input.value = ''
    let alerted = false
    for (const file of picked) {
      const rejection = validateFile(file, this.o.limits, this.attachments.length)
      if (!rejection) {
        this.attachments.push({ file, type: normalizeType(file.type), state: 'waiting', pct: 0 })
        continue
      }
      // ⚠ Only the FIRST rejection is announced — three alerts stacked on top of
      // each other would bury the one the reporter can act on — but the loop goes
      // on. Ending it here dropped every valid file picked after a bad one: a
      // reporter who multi-selected two screenshots and a 90 MB clip between them
      // was told about the clip and quietly lost the second screenshot.
      if (alerted) continue
      alerted = true
      if (rejection.reason === 'count') {
        this.showAlert('warn', s.tooManyTitle, s.tooManyBody(this.o.limits.maxFiles))
      } else if (rejection.reason === 'type') {
        this.showAlert('warn', s.wrongTypeTitle, `${file.name} · ${s.wrongTypeBody}`)
      } else if (rejection.reason === 'empty') {
        this.showAlert('warn', s.emptyFileTitle, `${file.name} · ${s.emptyFileBody}`)
      } else {
        this.showAlert(
          'danger',
          s.tooLargeTitle,
          `${file.name} · ${s.tooLargeBody(rejection.video, formatBytes(rejection.limit, s.lang), formatBytes(file.size, s.lang))}`,
        )
      }
    }
    this.syncFiles()
  }

  private syncFiles(): void {
    if (!this.filesEl || !this.pickBtn || !this.attachHintEl) return
    const s = this.o.strings
    const limits = this.o.limits
    // Locked from the moment Send is pressed. Removing a chip while the POST is
    // in flight would leave the slots the server signed pointing at the wrong
    // files — each one signed for a content type and an exact byte length, so
    // the mismatch surfaces as R2 refusing a file nothing was wrong with.
    const locked = this.phase !== 'form'
    // ⚠ The message freezes with them. `submit` reads the text before its waits
    // and posts that, so a correction typed during `presend` — up to the whole
    // of the server's dwell, on a young ticket — appeared in the field, was
    // accepted by the caret, and then never travelled: the reporter watched
    // their fix land and filed the version without it. `readOnly` rather than
    // `disabled`, so the text stays selectable and focus is not yanked out of
    // the control the reporter is looking at.
    if (this.msgInput) this.msgInput.readOnly = locked
    clear(this.filesEl)
    this.attachments.forEach((a, i) => {
      this.filesEl?.appendChild(
        el('div', {
          cls: 'sfb-chip',
          kids: [
            fileMark(a),
            fileText(a, s),
            el('button', {
              cls: 'sfb-iconbtn',
              attrs: {
                type: 'button',
                'aria-label': `${s.remove}: ${a.file.name}`,
                ...(locked ? { disabled: 'disabled' } : {}),
              },
              kids: [icon('x', 18)],
              on: {
                click: () => {
                  if (this.phase !== 'form') return
                  this.attachments.splice(i, 1)
                  this.syncFiles()
                },
              },
            }),
          ],
        }),
      )
    })
    const full = this.attachments.length >= limits.maxFiles
    this.pickBtn.disabled = full || locked
    clear(this.pickBtn)
    this.pickBtn.appendChild(icon('clip', 17))
    this.pickBtn.appendChild(el('span', { text: full ? s.attachFullBtn : s.attach }))
    this.attachHintEl.textContent = full
      ? s.attachHintFull(limits.maxFiles)
      : s.attachHint(
          limits.maxFiles,
          formatBytes(limits.maxImageBytes, s.lang),
          formatBytes(limits.maxVideoBytes, s.lang),
        )
  }

  private showAlert(tone: 'danger' | 'warn' | 'accent', title: string, body: string, action?: { label: string; onClick: () => void }): void {
    if (!this.alertHost) return
    clear(this.alertHost)
    this.alertHost.appendChild(
      el('div', {
        cls: `sfb-alert sfb-tone-${tone}`,
        attrs: { role: 'alert' },
        kids: [
          el('span', { attrs: { style: 'flex:none;margin-top:1px' }, kids: [icon(tone === 'accent' ? 'check' : 'alert', 16)] }),
          el('div', {
            attrs: { style: 'flex:1;min-width:0' },
            kids: [
              el('div', { cls: 'sfb-alert-title', text: title }),
              el('div', { cls: 'sfb-alert-body', text: body }),
              action
                ? el('button', {
                    cls: 'sfb-link',
                    attrs: { type: 'button' },
                    text: action.label,
                    on: { click: action.onClick },
                  })
                : null,
            ],
          }),
        ],
      }),
    )
  }

  private clearAlert(): void {
    if (this.alertHost) clear(this.alertHost)
  }

  // --- footer ---------------------------------------------------------------

  private renderFooter(): void {
    if (!this.foot) return
    const s = this.o.strings
    clear(this.foot)

    if (this.discardPrompt) {
      this.foot.appendChild(
        el('div', {
          // ⚠ role="alert", like every other treatment in showAlert. Escape
          // replaces the footer under a reporter whose focus is still in the
          // textarea, so nothing moves and nothing is announced: a screen-reader
          // user hears silence, reads it as "Escape did nothing", presses it
          // again — and the second press is the one that discards.
          attrs: { role: 'alert' },
          kids: [
            el('div', { attrs: { style: 'font-size:13px;font-weight:600;margin-bottom:10px' }, text: s.discardTitle }),
            el('div', {
              cls: 'sfb-btn-row',
              kids: [
                el('button', {
                  cls: 'sfb-btn',
                  attrs: { type: 'button' },
                  text: s.keepEditing,
                  on: {
                    click: () => {
                      this.discardPrompt = false
                      this.renderFooter()
                      this.msgInput?.focus()
                    },
                  },
                }),
                el('button', {
                  cls: 'sfb-btn sfb-btn-quiet',
                  attrs: { type: 'button' },
                  text: s.discard,
                  on: { click: () => this.close() },
                }),
              ],
            }),
          ],
        }),
      )
    } else if (this.phase === 'done') {
      this.foot.appendChild(
        el('button', {
          cls: 'sfb-btn',
          attrs: { type: 'button' },
          text: s.close,
          on: { click: () => this.close() },
        }),
      )
    } else if (this.phase !== 'uploading') {
      // `presend` reads as sending, because from the reporter's side it is: they
      // pressed Send and the widget is working on it.
      const sending = this.phase !== 'form'
      this.foot.appendChild(
        el('button', {
          cls: 'sfb-btn',
          attrs: { type: 'button', ...(sending ? { disabled: 'disabled' } : {}) },
          text: sending ? s.sending : s.send,
          on: { click: () => this.submit() },
        }),
      )
    }
    // ⚠ `clear` above can remove the control that had focus — pressing Send and
    // watching it become a disabled "Sending…" is the ordinary case, not an edge
    // one. Without this, focus lands on <body> for the rest of the opening.
    this.holdFocus()
  }

  // --- submit ---------------------------------------------------------------

  private async submit(): Promise<void> {
    const s = this.o.strings
    if (this.phase !== 'form') return
    // Captured before the first await: everything below belongs to THIS opening
    // and must not touch a later one.
    const gen = this.generation
    const message = this.message.trim()
    if (!message) {
      this.showAlert('warn', s.msgLabel, s.msgRequired)
      this.msgInput?.focus()
      return
    }
    // ⚠ The phase moves before the first await, not after it: everything below
    // pairs the slots the server signs with `this.attachments` BY INDEX, so the
    // list must stop being editable the moment Send is pressed — and a second
    // click must not start a second submission.
    this.clearAlert()
    this.phase = 'presend'
    // ⚠ Before the footer is drawn. A discard prompt raised by a ✕ is answered
    // by choosing to send — leaving it set would render the question over the
    // sending phase, the uploads and the success screen, with a Discard button
    // that closes mid-upload. A reporter pressing Send IS an answer; a timer
    // resuming below is not, which is what the check after the waits is for.
    this.discardPrompt = false
    this.renderFooter()
    this.syncFiles()

    // The two waits every send owes, in the one place a send starts. The ticket
    // has to be here, and then it has to be old enough: the server refuses one
    // younger than `min_dwell_ms` as a script, and that refusal is a 422 the
    // reporter reads as "the connection dropped". Both are `presend`, so a ✕
    // during either asks before throwing the report away.
    //
    // ⚠ The dwell is owed on the FIRST send too, not only on a retry. `open()`
    // refreshes a ticket near its expiry and `failSend` mints a replacement, so
    // a reporter who pastes a prepared sentence — or who presses the footer's
    // own Send after a failure rather than the alert's button — posts a ticket
    // minted seconds ago and is told the send failed, with nothing wrong.
    await this.o.api.ticketSettled()
    const owed = this.o.api.ticketOwedMs()
    if (owed > 0) await new Promise((resolve) => setTimeout(resolve, owed))
    // ⚠ Nothing is cleared on the abandoned path: the timer above belongs to an
    // opening that is over, and `reset()` has already returned the current one
    // to `form`. Every write below this line is on the far side of `live`.
    if (!this.live(gen)) return
    // A ✕ during either wait raised the discard question and nobody has answered
    // it. Sending now would be the widget answering its own modal question — the
    // same failure as discarding without asking, from the other side. Back to the
    // form leaves the decision with the reporter: Keep editing restores the
    // footer's own Send, Discard closes.
    if (this.discardPrompt) {
      this.backToForm()
      return
    }
    const ticket = this.o.api.ticket()
    if (!ticket) {
      this.backToForm()
      this.failSend('network')
      return
    }

    const ctx = this.o.context()
    const sendConsole = this.o.consoleCapture && !this.consoleOptOut
    const payload: Submission = {
      message,
      kind: this.kind,
      ticket,
      reporter_label: ctx.reporterLabel,
      page_url: ctx.pageUrl,
      referrer: ctx.referrer,
      viewport: ctx.viewport,
      locale: ctx.locale,
      app_release: ctx.appRelease,
      console_tail: null,
      last_error: sendConsole ? this.o.capture.lastError() : null,
      website: this.honeypot?.value ?? '',
      files: this.attachments.map((a) => ({ content_type: a.type, byte_size: a.file.size })),
    }
    if (sendConsole) payload.console_tail = this.fitConsoleTail(payload)

    // From here the report IS on its way, so closing loses nothing that matters
    // and no longer asks. The footer does not change — `presend` and `sending`
    // draw the same disabled button — so there is nothing to re-render.
    this.phase = 'sending'
    const result = await this.o.api.submit(payload)
    if (!this.live(gen)) return
    if (!result.ok) {
      this.backToForm()
      if (result.kind === 'rate') {
        const minutes = Math.max(1, Math.ceil(result.retryAfterSeconds / 60))
        // ⚠ No ticket refresh on this path: a 429 never reached validation, so
        // the one the dialog holds is unspent and already old enough to use.
        //
        // ⚠ And no action button. The wait is the whole content of this alert:
        // a button labelled "try again in 5 minutes" that submits the moment it
        // is pressed answers with this same alert, forever. The footer's own
        // Send is still there for when the wait is over, and the text the
        // reporter typed is still in the field beside it.
        this.showAlert('warn', s.rateTitle, `${s.rateBody} ${s.retryIn(minutes)}`)
        return
      }
      this.failSend(result.kind === 'tooLarge' ? 'tooLarge' : 'network')
      return
    }

    // The ticket is spent. A new one is fetched now rather than at the next open,
    // so it has aged past MIN_DWELL_MS by the time anyone could use it.
    void this.o.api.refreshTicket()

    this.ref = result.accepted.ref
    const slots = result.accepted.uploads
    if (this.attachments.length === 0) {
      this.renderSuccess([])
      return
    }
    if (slots.length === 0) {
      // The report was accepted but no slot could be signed, so there is nowhere
      // to put the files. That is the "report landed, file did not" case, and the
      // reporter is told which files rather than left to assume they arrived.
      for (const a of this.attachments) a.state = 'failed'
      this.renderSuccess(this.attachments.map((a) => a.file.name))
      return
    }
    await this.runUploads(slots, gen)
  }

  /** backToForm is the single transition that ends a send without one being
   *  stored: a failure, and the standing-discard-question case above. It undoes
   *  everything `presend` did — the phase itself, the footer's disabled button,
   *  and the attachment controls that phase locked — so nothing about a send
   *  survives it. Drawing the footer is part of that: with the question still
   *  standing it draws the question, which is what a reporter who has not
   *  answered it should still be looking at. */
  private backToForm(): void {
    this.phase = 'form'
    this.renderFooter()
    this.syncFiles()
  }

  /**
   * fitConsoleTail trims the console lines to whatever the rest of the payload
   * leaves of the server's whole-body cap.
   *
   * The tail is measured against the payload it will travel in rather than
   * against a fixed budget, because the field it competes with — the message —
   * is the one that must never be the thing dropped.
   *
   * ⚠ The cap is `max_text_bytes` from the config, not a constant mirroring the
   * server's default. It is smaller than the sum of the field limits the same
   * contract allows — a 4 000-character message is up to 8 000 bytes in Czech on
   * its own — so a report over it on its text alone still 413s, and says so.
   */
  private fitConsoleTail(payload: Submission): string[] {
    const lines = this.o.capture.lines()
    if (lines.length === 0) return lines
    const rest = utf8Length(JSON.stringify({ ...payload, console_tail: [] }))
    return trimToBudget(lines, Math.max(0, this.o.limits.maxTextBytes - rest - BODY_SLACK))
  }

  /**
   * failSend is the shared treatment for a dropped connection, a 5xx, a 422 and
   * a refused key: all outside the reporter's control, and the one thing they
   * need to hear is that the text they typed is still there. `tooLarge` is the
   * exception — that one IS theirs to fix, so it says which thing to shorten.
   *
   * ⚠ The ticket is refreshed HERE, when the failure is shown, rather than when
   * the retry is pressed. A ticket minted at the moment of the click is younger
   * than the server's minimum dwell and is refused every time; minting it now
   * lets it age while the reporter reads this, and `submit` waits out whatever
   * is left of the dwell either way — through this button or through the
   * footer's own Send, which is the same path.
   */
  private failSend(kind: 'network' | 'tooLarge'): void {
    const s = this.o.strings
    const large = kind === 'tooLarge'
    void this.o.api.refreshTicket()
    this.showAlert('danger', large ? s.tooLongTitle : s.sendFailTitle, large ? s.tooLongBody : s.sendFailBody, {
      label: s.sendAgain,
      // Returned, not `void`ed: `el` only catches a rejection it can see.
      onClick: () => this.submit(),
    })
  }

  private async runUploads(slots: UploadSlot[], gen: number): Promise<void> {
    this.phase = 'uploading'
    this.renderUploading()

    // ⚠ Sequential, one file at a time (V3-D38): a 50 MB video and two
    // screenshots racing each other on household wifi is slower than the same
    // three in series, and gives the reporter no honest progress to look at.
    for (let i = 0; i < slots.length && i < this.attachments.length; i++) {
      if (!this.live(gen)) return
      const attachment = this.attachments[i]
      attachment.state = 'uploading'
      attachment.pct = 0
      this.setUploadStatus(i)
      this.updateUploadRow(i)
      let ok = await this.putOnce(slots[i], attachment, i)
      if (!ok && this.live(gen)) ok = await this.putOnce(slots[i], attachment, i) // one retry, then abandoned
      attachment.state = ok ? 'done' : 'failed'
      this.updateUploadRow(i)
    }
    this.activeUpload = null
    if (!this.live(gen)) return

    // The claim runs whatever happened above: it is what turns an uploaded object
    // into a `stored` attachment, and skipping it after a partial failure would
    // leave the files that DID arrive pending until the sweep deleted them.
    if (this.ref) await this.o.api.claim(this.ref)
    if (!this.live(gen)) return
    this.renderSuccess(this.attachments.filter((a) => a.state !== 'done').map((a) => a.file.name))
  }

  private putOnce(slot: UploadSlot, attachment: Attachment, index: number): Promise<boolean> {
    const upload = this.o.api.put(slot, attachment.file, (pct) => {
      attachment.pct = pct
      this.updateUploadRow(index)
    })
    this.activeUpload = upload
    return upload.promise
  }

  /** renderUploading builds the progress view ONCE. Progress then writes to the
   *  rows it keeps references to — re-rendering on every progress event would
   *  rewrite the polite live region several times a second, which a screen reader
   *  reads out as a stream of interruptions. */
  private renderUploading(): void {
    if (!this.body) return
    const s = this.o.strings
    clear(this.body)
    if (this.foot) clear(this.foot)
    this.uploadRows = []

    this.body.appendChild(
      el('div', {
        cls: 'sfb-quote',
        kids: [
          el('div', { cls: 'sfb-quote-label', text: s.yourReport }),
          el('div', { cls: 'sfb-quote-text', text: this.message.trim() }),
        ],
      }),
    )
    this.uploadStatus = el('span', { text: s.uploadingN(1, this.attachments.length) })
    this.body.appendChild(
      el('div', {
        cls: 'sfb-status',
        attrs: { role: 'status', 'aria-live': 'polite' },
        kids: [el('span', { cls: 'sfb-spinner' }), this.uploadStatus],
      }),
    )

    const list = el('div', { cls: 'sfb-files' })
    this.attachments.forEach((a) => {
      const label = el('span', { cls: 'sfb-chip-state', text: s.fileWaiting })
      const fill = el('span', { attrs: { style: 'width:0%' } })
      const bar = el('div', { cls: 'sfb-bar', kids: [fill] })
      bar.style.display = 'none'
      const mark = fileMark(a)
      list.appendChild(
        el('div', {
          attrs: { style: 'border:1px solid var(--sfb-hairline);border-radius:var(--sfb-radius-sm);padding:9px 11px' },
          kids: [
            el('div', {
              attrs: { style: 'display:flex;align-items:center;gap:10px' },
              kids: [
                mark,
                fileText(a, s),
                label,
              ],
            }),
            bar,
          ],
        }),
      )
      this.uploadRows.push({ label, bar, fill, mark })
    })
    this.body.appendChild(list)
    this.body.appendChild(el('div', { cls: 'sfb-hint', text: s.uploadNote }))
    this.holdFocus()
  }

  private setUploadStatus(index: number): void {
    if (this.uploadStatus) {
      this.uploadStatus.textContent = this.o.strings.uploadingN(index + 1, this.attachments.length)
    }
  }

  private updateUploadRow(index: number): void {
    const row = this.uploadRows[index]
    const a = this.attachments[index]
    if (!row || !a) return
    const s = this.o.strings
    const tone =
      a.state === 'failed' ? 'var(--sfb-danger)' : a.state === 'waiting' ? 'var(--sfb-muted)' : 'var(--sfb-accent)'
    // Never colour alone: each state carries a word as well as a tone.
    row.label.textContent =
      a.state === 'done' ? s.fileDone : a.state === 'failed' ? s.fileFailed : a.state === 'waiting' ? s.fileWaiting : `${a.pct} %`
    row.label.style.color = tone
    row.mark.style.color = tone
    row.bar.style.display = a.state === 'uploading' ? 'block' : 'none'
    row.fill.style.width = `${a.pct}%`
  }

  private renderSuccess(failedNames: string[]): void {
    if (!this.body || !this.foot) return
    const s = this.o.strings
    this.phase = 'done'
    clear(this.body)
    this.body.className = 'sfb-success'

    const partial = failedNames.length > 0
    this.body.appendChild(el('div', { cls: 'sfb-success-mark', kids: [icon('check', 26)] }))
    this.body.appendChild(el('div', { cls: 'sfb-success-title', text: partial ? s.uploadFailedTitle : s.successTitle }))
    // ⚠ Accent, not red. The report exists; only a file did not arrive. This
    // reads as a variant of success because that is what it is (design §4.5, row 5).
    this.body.appendChild(
      el('p', {
        cls: 'sfb-success-body',
        attrs: partial ? { role: 'status', 'aria-live': 'polite' } : {},
        text: partial ? s.uploadFailedBody(failedNames) : s.successBody,
      }),
    )

    const code = el('div', { cls: 'sfb-ref-code', text: this.ref ?? '' })
    const copyBtn = el('button', {
      cls: 'sfb-ref-copy',
      attrs: { type: 'button' },
      kids: [icon('copy', 16), el('span', { text: s.copy })],
      on: {
        click: () => {
          const label = copyBtn.querySelector('span')
          void navigator.clipboard
            ?.writeText(this.ref ?? '')
            .then(() => {
              if (label) label.textContent = s.copied
            })
            .catch(() => {
              // Clipboard permission is the host page's business, and the code is
              // on screen in 32px type either way.
            })
        },
      },
    })
    this.body.appendChild(
      el('div', {
        cls: 'sfb-ref',
        kids: [el('div', { cls: 'sfb-ref-label', text: s.refLabel }), code, copyBtn],
      }),
    )
    this.body.appendChild(el('p', { cls: 'sfb-ref-help', text: s.refHelp }))
    this.renderFooter()
    copyBtn.focus()
  }
}
