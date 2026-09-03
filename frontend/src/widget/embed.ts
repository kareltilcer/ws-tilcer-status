import { resolvePosition } from './launcher'
import type { Position } from './types'

/** Embed is the `<script>` tag's contract with the host page. It may gain
 *  attributes and never loses one — the same promise `StatusFeedback.open()`
 *  makes (V3-D56). */
export interface Embed {
  site: string
  key: string
  /** base is the origin the bundle was served from. */
  base: string
  lang: string | null
  reporter: string | null
  release: string | null
  position: Position
  launcher: boolean
}

/**
 * readEmbed parses the embedding script tag.
 *
 * `script` is `document.currentScript`, which must be read at module scope while
 * the bundle is still executing — it is null by the time any callback runs. The
 * query fallback covers a host that copies the bundle somewhere its
 * `currentScript` cannot be read, such as an inline eval.
 */
export function readEmbed(script: HTMLScriptElement | null): Embed | null {
  const node = script ?? document.querySelector<HTMLScriptElement>('script[src*="/widget/v"][data-site]')
  if (!node) return null
  const site = node.getAttribute('data-site')
  const key = node.getAttribute('data-key')
  if (!site || !key) return null
  let base: string
  try {
    // The widget calls back to the origin it was served from, so an embed never
    // names the API host twice and a staging copy talks to staging.
    base = new URL(node.src, window.location.href).origin
  } catch {
    return null
  }
  return {
    site,
    key,
    base,
    lang: node.getAttribute('data-lang'),
    reporter: node.getAttribute('data-reporter'),
    release: node.getAttribute('data-release'),
    position: resolvePosition(node.getAttribute('data-position')),
    // ⚠ `data-launcher="none"` suppresses the floating button and nothing else.
    // The programmatic API stays, because that is the whole point of the flag:
    // `home` puts reporting in its own nav, where household members will actually
    // find it (V3-D56).
    launcher: node.getAttribute('data-launcher') !== 'none',
  }
}
