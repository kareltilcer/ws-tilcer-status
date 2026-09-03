import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WidgetConfig } from './types'

// The widget mounts into a CLOSED shadow root, which is exactly what makes it
// unreadable from a host page — and from a test. Opening it here, at the DOM
// level, is how the boot behaviour stays assertable without the production code
// carrying a test hook it would then have to keep.
const realAttachShadow = Element.prototype.attachShadow
function openShadowRoots(): void {
  Element.prototype.attachShadow = function attachShadow(this: Element, init: ShadowRootInit) {
    return realAttachShadow.call(this, { ...init, mode: 'open' })
  }
}

function embedScript(attrs: Record<string, string>): HTMLScriptElement {
  const script = document.createElement('script')
  script.setAttribute('src', 'https://status.tilcer.cz/widget/v1.js')
  for (const [k, v] of Object.entries(attrs)) script.setAttribute(k, v)
  document.head.appendChild(script)
  return script
}

function respondWith(config: WidgetConfig | { status: number }): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      if ('status' in config) return new Response('{}', { status: config.status })
      return new Response(JSON.stringify(config), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }),
  )
}

/** boot loads the entry module, which runs on import, and drains the async chain
 *  it starts. */
async function boot(): Promise<void> {
  await import('./main')
  for (let i = 0; i < 12; i++) await Promise.resolve()
}

const enabled: WidgetConfig = {
  enabled: true,
  ticket: 'ticket.sig',
  kinds: ['bug', 'idea', 'other'],
  max_files: 3,
  max_image_bytes: 10 * 1024 * 1024,
  max_video_bytes: 50 * 1024 * 1024,
  accept: ['image/png', 'video/mp4'],
  console_capture: false,
  strings_version: 1,
}

function container(): HTMLElement | null {
  return document.querySelector<HTMLElement>('[data-status-feedback]')
}

function shadow(): ShadowRoot | null {
  return container()?.shadowRoot ?? null
}

beforeEach(() => {
  vi.resetModules()
  document.head.innerHTML = ''
  document.body.innerHTML = ''
  document.documentElement.removeAttribute('lang')
  delete window.StatusFeedback
  openShadowRoots()
})

afterEach(() => {
  Element.prototype.attachShadow = realAttachShadow
  vi.unstubAllGlobals()
})

describe('boot', () => {
  it('renders the launcher when the site is enabled', async () => {
    embedScript({ 'data-site': 'home', 'data-key': 'wk_test' })
    respondWith(enabled)
    await boot()

    const launcher = shadow()?.querySelector('button.sfb-launcher')
    expect(launcher).not.toBeNull()
    // Czech is the default, and the launcher is a real button with a name.
    expect(launcher?.getAttribute('aria-label')).toBe('Nahlásit problém')
    expect(launcher?.getAttribute('aria-haspopup')).toBe('dialog')
    expect(launcher?.tagName).toBe('BUTTON')
  })

  it('follows data-lang for the accessible name', async () => {
    embedScript({ 'data-site': 'home', 'data-key': 'wk_test', 'data-lang': 'en' })
    respondWith(enabled)
    await boot()
    expect(shadow()?.querySelector('button.sfb-launcher')?.getAttribute('aria-label')).toBe('Report a problem')
  })

  it('renders NOTHING AT ALL for a disabled site', async () => {
    embedScript({ 'data-site': 'home', 'data-key': 'wk_test' })
    respondWith({ enabled: false })
    await boot()
    // ⚠ V3-D35: not a hidden button, not a disabled one — no launcher, and no
    // element on the host page at all.
    expect(container()).toBeNull()
  })

  it('renders nothing when the key is refused, and nothing when the network fails', async () => {
    embedScript({ 'data-site': 'home', 'data-key': 'wk_wrong' })
    respondWith({ status: 401 })
    await boot()
    expect(container()).toBeNull()

    vi.resetModules()
    document.head.innerHTML = ''
    embedScript({ 'data-site': 'home', 'data-key': 'wk_test' })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new Error('offline')
      }),
    )
    await boot()
    expect(container()).toBeNull()
  })

  it('does nothing at all without a site and key, and never throws into the host', async () => {
    embedScript({})
    respondWith(enabled)
    await expect(boot()).resolves.toBeUndefined()
    expect(container()).toBeNull()
    // The API still exists, so a host menu item wired to it is a no-op rather
    // than a TypeError.
    expect(() => window.StatusFeedback?.open()).not.toThrow()
  })
})

describe('StatusFeedback.open', () => {
  it('opens the dialog', async () => {
    embedScript({ 'data-site': 'home', 'data-key': 'wk_test' })
    respondWith(enabled)
    await boot()

    expect(shadow()?.querySelector('[role="dialog"]')).toBeNull()
    window.StatusFeedback?.open()
    expect(shadow()?.querySelector('[role="dialog"]')).not.toBeNull()
  })

  it('still works when data-launcher="none" suppresses the floating button', async () => {
    embedScript({ 'data-site': 'home', 'data-key': 'wk_test', 'data-launcher': 'none' })
    respondWith(enabled)
    await boot()

    expect(shadow()?.querySelector('button.sfb-launcher')).toBeNull()
    window.StatusFeedback?.open()
    expect(shadow()?.querySelector('[role="dialog"]')).not.toBeNull()
  })

  it('is callable before the configuration has arrived, and opens once it has', async () => {
    embedScript({ 'data-site': 'home', 'data-key': 'wk_test' })
    let resolveConfig: (r: Response) => void = () => {}
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>((resolve) => { resolveConfig = resolve })),
    )
    const booted = import('./main')
    await Promise.resolve()
    // The host's menu item is clicked while the config request is still open.
    expect(() => window.StatusFeedback?.open()).not.toThrow()

    resolveConfig(new Response(JSON.stringify(enabled), { status: 200 }))
    await booted
    for (let i = 0; i < 12; i++) await Promise.resolve()
    expect(shadow()?.querySelector('[role="dialog"]')).not.toBeNull()
  })
})
