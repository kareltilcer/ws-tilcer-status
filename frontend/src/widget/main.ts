// The widget's entry point: read the embed's attributes, ask the server what it
// may do, and only then put anything on the page.
//
// ⚠ Nothing here may throw into the host app (V3-D37). Every entry point — boot,
// the click handlers, the fetch chain — is wrapped, and every failure is silent
// except the dialog's own error states. A widget that crashed `home` while
// somebody was reporting a bug in `home` would be a small masterpiece.

import { claimUploads, fetchWidgetConfig, putObject, submitReport, type ApiTarget } from './api'
import { emptyCapture, installConsoleCapture, type ConsoleCapture } from './consoleTail'
import { FeedbackDialog, resolveKinds, type ReportContext } from './dialog'
import { limitsFrom } from './files'
import { browserLabel, viewportText } from './format'
import { resolveLang, STRINGS } from './i18n'
import { readEmbed } from './embed'
import { createLauncher, setLauncherExpanded } from './launcher'
import { WIDGET_CSS } from './styles'

/** TICKET_MAX_AGE_MS is when a held ticket is refreshed. The server expires them
 *  at 30 minutes; refreshing at 25 leaves the dialog with a usable one and keeps
 *  it old enough to satisfy MIN_DWELL_MS, which a ticket minted at the moment the
 *  dialog opens would not. */
const TICKET_MAX_AGE_MS = 25 * 60 * 1000

/** SCRIPT is read at module scope, while the bundle is still executing —
 *  `document.currentScript` is null by the time any callback runs. */
const SCRIPT = (document.currentScript as HTMLScriptElement | null) ?? null

declare global {
  interface Window {
    StatusFeedback?: { open: () => void }
  }
}

function boot(): void {
  // A second embed on the same page is a mistake, not a feature: two launchers in
  // the same corner, two config fetches, two tickets.
  if (window.StatusFeedback) return

  const embed = readEmbed(SCRIPT)
  let open: (() => void) | null = null
  let queued = false

  // ⚠ The public API exists from the first line, before the configuration is
  // known (V3-D56). A host that wires `StatusFeedback.open()` to a menu item must
  // not have to care whether the config round-trip has finished — and if it never
  // does, or the site is disabled, `open()` stays a silent no-op rather than
  // becoming a button that fails when pressed.
  window.StatusFeedback = {
    open() {
      try {
        if (open) open()
        else queued = true
      } catch {
        // never into the host
      }
    },
  }
  if (!embed) return

  const target: ApiTarget = { base: embed.base, site: embed.site, key: embed.key }
  void (async () => {
    try {
      const cfg = await fetchWidgetConfig(target)
      // Disabled site, unknown key, rate limit or dead network: render nothing at
      // all (V3-D35). There is no screen for this, and that is the design.
      if (!cfg.enabled || !cfg.ticket) return
      await whenBodyReady()

      const strings = STRINGS[resolveLang(embed.lang, document.documentElement.getAttribute('lang'))]
      const limits = limitsFrom(cfg)
      const consoleCapture = cfg.console_capture === true
      const capture: ConsoleCapture = consoleCapture ? installConsoleCapture() : emptyCapture

      let ticket: string | null = cfg.ticket
      let ticketAt = Date.now()
      let refreshing: Promise<void> | null = null

      /**
       * refreshTicket replaces the held ticket, at most one request at a time.
       *
       * ⚠ A failed refresh KEEPS the ticket it has. `fetchWidgetConfig` collapses
       * a disabled site, a 429 and a dead network into the same `{enabled:false}`
       * (V3-D35), so it cannot tell "gone" from "not right now" — and dropping
       * the ticket on the second reading would let one blip kill the dialog for
       * the life of the page, with the launcher still on screen. A site that
       * really was disabled answers the submit with a 403 instead.
       */
      const refreshTicket = (): Promise<void> => {
        if (refreshing) return refreshing
        refreshing = (async () => {
          try {
            const next = await fetchWidgetConfig(target)
            if (next.enabled && next.ticket) {
              ticket = next.ticket
              ticketAt = Date.now()
            }
          } finally {
            refreshing = null
          }
        })()
        return refreshing
      }

      const container = document.createElement('div')
      container.setAttribute('data-status-feedback', embed.site)
      // Inline, because a host page's own `div { … }` rules beat anything a
      // `:host` rule inside the shadow root can say. Fixed and zero-sized so the
      // container cannot add so much as a line box to the host's layout.
      container.setAttribute('style', 'all:initial;position:fixed;top:0;left:0;width:0;height:0')
      const shadow = container.attachShadow({ mode: 'closed' })
      const style = document.createElement('style')
      style.textContent = WIDGET_CSS
      shadow.appendChild(style)
      document.body.appendChild(container)

      let launcher: HTMLButtonElement | null = null
      let returnFocusTo: HTMLElement | null = null

      const dialog = new FeedbackDialog({
        root: shadow,
        strings,
        limits,
        kinds: resolveKinds(cfg.kinds),
        consoleCapture,
        capture,
        context: (): ReportContext => ({
          pageUrl: window.location.href,
          referrer: document.referrer || null,
          viewport: viewportText(window.innerWidth, window.innerHeight),
          locale: navigator.language || null,
          appRelease: embed.release,
          reporterLabel: embed.reporter,
          browserLabel: browserLabel(navigator.userAgent),
        }),
        api: {
          submit: (body) => submitReport(target, body),
          put: (slot, file, onProgress) => putObject(slot, file, onProgress),
          claim: (ref) => claimUploads(target, ref),
          ticket: () => ticket,
          ticketAgeMs: () => Date.now() - ticketAt,
          refreshTicket,
          // ⚠ open() starts a refresh for a ticket near its expiry and does not
          // wait for it. Reading the ticket without settling that first would
          // submit the very ticket the refresh is replacing.
          ticketSettled: () => refreshing ?? Promise.resolve(),
        },
        onClosed: () => {
          setLauncherExpanded(launcher, false)
          // Closing returns focus where it came from (WCAG 2.4.3): whatever the
          // host had focused when open() was called, and the launcher otherwise.
          //
          // ⚠ `returnFocusTo` is read from the LIGHT DOM, so a click on our own
          // launcher — which lives in a closed shadow root — records the
          // container element, not the button. That container is
          // `all:initial;width:0` with no tabindex, so focusing it would drop
          // focus to <body>; it is exactly the case the launcher branch is for.
          // Preferring the launcher outright, on the other hand, makes this
          // whole variable dead whenever a launcher is rendered — which is the
          // default — and strands the keyboard user who opened the dialog from
          // the host's own menu item.
          const fromHost = returnFocusTo && returnFocusTo !== container && returnFocusTo !== document.body
          const back = fromHost ? returnFocusTo : launcher
          try {
            back?.focus()
          } catch {
            // an element that has since been removed from the host's DOM
          }
        },
      })

      open = () => {
        if (dialog.isOpen) return
        if (Date.now() - ticketAt > TICKET_MAX_AGE_MS) void refreshTicket()
        returnFocusTo = (document.activeElement as HTMLElement | null) ?? null
        setLauncherExpanded(launcher, true)
        dialog.open()
      }

      if (embed.launcher) {
        launcher = createLauncher(strings, embed.position, () => open?.())
        shadow.appendChild(launcher)
      }
      if (queued) open()
    } catch {
      // Anything unexpected leaves the host page exactly as it was.
    }
  })()
}

function whenBodyReady(): Promise<void> {
  if (document.body) return Promise.resolve()
  return new Promise((resolve) => {
    document.addEventListener('DOMContentLoaded', () => resolve(), { once: true })
  })
}

try {
  boot()
} catch {
  // never into the host
}
