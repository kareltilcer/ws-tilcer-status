// A four-function DOM layer. There is no framework here on purpose (FR-24): the
// SPA's React 19 is not a dependency the monitored apps should inherit, and the
// widget has to survive a host page that already has a different React.

export interface ElOptions {
  cls?: string
  text?: string
  attrs?: Record<string, string | null>
  kids?: (Node | null | false | undefined)[]
  on?: Partial<Record<keyof HTMLElementEventMap, (e: Event) => void>>
}

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  opts: ElOptions = {},
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag)
  if (opts.cls) node.className = opts.cls
  // ⚠ Always textContent, never innerHTML: `data-reporter` and the console tail
  // are strings from a page this code does not control.
  if (opts.text !== undefined) node.textContent = opts.text
  for (const [k, v] of Object.entries(opts.attrs ?? {})) {
    if (v === null) node.removeAttribute(k)
    else node.setAttribute(k, v)
  }
  // ⚠ Every listener is wrapped, and this is the ONLY place it happens (V3-D37).
  // A handler that throws inside a host page's click reaches that page's
  // `window.onerror`, and an async one that rejects reaches its
  // `unhandledrejection` — where, on a console-capture site, this widget's own
  // capture then files it as the host's error. Wrapping at each call site instead
  // would mean the launcher, the most-clicked element v3 ships, being the one
  // that got forgotten.
  for (const [event, handler] of Object.entries(opts.on ?? {})) {
    node.addEventListener(event, (e: Event) => {
      try {
        const r = (handler as (e: Event) => unknown)(e)
        if (r instanceof Promise) r.catch(() => {})
      } catch {
        // never into the host
      }
    })
  }
  for (const kid of opts.kids ?? []) {
    if (kid) node.appendChild(kid)
  }
  return node
}

export function clear(node: Element): void {
  while (node.firstChild) node.removeChild(node.firstChild)
}

const SVG_NS = 'http://www.w3.org/2000/svg'

const ICONS: Record<string, string> = {
  bubble:
    '<path d="M8 2l1.5 1.5"/><path d="M16 2l-1.5 1.5"/><path d="M9 7h6a3 3 0 0 1 3 3v3a6 6 0 0 1-12 0v-3a3 3 0 0 1 3-3Z"/><path d="M12 20v-9"/><path d="M6 13H3"/><path d="M21 13h-3"/><path d="M6 9L3.5 7"/><path d="M18 9l2.5-2"/><path d="M6 17l-2.5 2"/><path d="M18 17l2.5 2"/>',
  x: '<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>',
  clip: '<path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/>',
  image:
    '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>',
  video: '<polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2"/>',
  check: '<polyline points="20 6 9 17 4 12"/>',
  alert:
    '<path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>',
  info: '<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>',
  copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
  chevD: '<polyline points="6 9 12 15 18 9"/>',
  chevU: '<polyline points="18 15 12 9 6 15"/>',
}

export type IconName = keyof typeof ICONS

/** icon returns one 24-grid stroke glyph. Every icon is decorative — the meaning
 *  is always carried by adjacent text as well, so none of them is ever the only
 *  signal (WCAG 1.4.1). */
export function icon(name: IconName, size = 18): SVGElement {
  const svg = document.createElementNS(SVG_NS, 'svg')
  svg.setAttribute('width', String(size))
  svg.setAttribute('height', String(size))
  svg.setAttribute('viewBox', '0 0 24 24')
  svg.setAttribute('fill', 'none')
  svg.setAttribute('stroke', 'currentColor')
  svg.setAttribute('stroke-width', '1.8')
  svg.setAttribute('stroke-linecap', 'round')
  svg.setAttribute('stroke-linejoin', 'round')
  svg.setAttribute('aria-hidden', 'true')
  svg.innerHTML = ICONS[name] ?? ''
  return svg
}
