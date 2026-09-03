import { el, icon } from './dom'
import type { Strings } from './i18n'
import type { Position } from './types'

const POSITIONS: Position[] = ['bottom-right', 'bottom-left', 'top-right', 'top-left']

export function resolvePosition(attr: string | null): Position {
  const v = (attr ?? '').trim() as Position
  return POSITIONS.includes(v) ? v : 'bottom-right'
}

/**
 * createLauncher builds the floating button.
 *
 * ⚠ It is a real `<button>` with an accessible name in the site's language, not
 * a div (WCAG 4.1.2) — this is the single most-seen element in v3 and it is used
 * by people who did not choose to use it. Icon-only at rest so it does not become
 * furniture people resent; it expands to a labelled pill on hover and on
 * focus-visible so it is findable the moment somebody looks for it.
 */
export function createLauncher(strings: Strings, position: Position, onClick: () => void): HTMLButtonElement {
  const button = el('button', {
    cls: `sfb-launcher sfb-pos-${position}`,
    attrs: {
      type: 'button',
      'aria-label': strings.title,
      'aria-haspopup': 'dialog',
      'aria-expanded': 'false',
    },
    kids: [icon('bubble', 20), el('span', { cls: 'sfb-launcher-label', text: strings.title })],
    on: { click: () => onClick() },
  }) as HTMLButtonElement
  return button
}

export function setLauncherExpanded(button: HTMLButtonElement | null, expanded: boolean): void {
  button?.setAttribute('aria-expanded', String(expanded))
}
