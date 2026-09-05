// The widget's entire appearance, as one string injected into a closed shadow
// root.
//
// ⚠ These tokens are NOT derived from status's oklch palette and are not exposed
// for host theming (V3-D55). Status's panel is a dark oklch surface with a
// light-blue accent; dropped onto `home`'s light pages it reads as breakage, not
// as another service. A `--sfb-*` variable contract would also be design surface
// to specify, document and support for three apps styled by one person. So: one
// appearance, asserted on every host — a white surface, one deep-teal accent, one
// radius pair, a system font stack.
//
// Eight rules inside carry weight beyond their looks and have no comment of
// their own, because everything in the template literal ships to every host
// page:
//
//   :host > *                carries the font stack and the line height, and
//                            ⚠ `:host` must not. The host element also has an
//                            INLINE `all:initial` (main.ts), and an inline style
//                            is an outer-context declaration: for normal rules
//                            the outer context beats the inner one, so
//                            `:host { font-family: var(--sfb-font) }` lost to
//                            `font-family: initial` and the whole widget
//                            inherited the UA's default serif at line-height
//                            `normal` — on every host page, measured in Chrome.
//                            Custom properties survive `all`, so the token
//                            itself is still readable from here; only the two
//                            inherited declarations have to be applied to
//                            elements the host cannot reach.
//
//   .sfb-wrap                is the backdrop AND the centring wrapper, one
//                            element. Two full-viewport fixed layers would mean
//                            the upper one swallowing every click meant for the
//                            lower, so a separate `.sfb-scrim` beneath it could
//                            never receive the click-outside-to-close it carried.
//                            ⚠ Its `height: 100dvh` is not a longer spelling of
//                            the `inset: 0` above it. On a phone those are two
//                            different boxes: `inset` resolves against the LAYOUT
//                            viewport, which does not shrink for the chrome a
//                            phone browser draws over it — so the bottom of this
//                            wrapper, and with it the bottom-aligned sheet, and
//                            with THAT the footer Send lives in, sat below the
//                            visible screen. A browser too old for `dvh` drops the
//                            declaration whole and keeps the old behaviour, which
//                            is why it is a second declaration and not a
//                            replacement.
//                            ⚠ AND `dvh` DOES NOT COVER THE ON-SCREEN KEYBOARD.
//                            Chrome defaults to
//                            `interactive-widget=resizes-visual`, which shrinks the
//                            VISUAL viewport only, and no CSS unit tracks that one
//                            — so a reporter who has just typed a sentence, which
//                            is every reporter who gets that far, still cannot see
//                            the button that sends it. dialog.ts drives this height
//                            from `window.visualViewport` while the dialog is open;
//                            these two rules are the floor under it.
//
//   .sfb-launcher            icon-only at rest, expanding to a labelled pill on
//                            hover and focus — it is seen ten thousand times more
//                            often than it is clicked, so it earns no colour.
//   @media (max-width:479px), the dialog becomes a bottom sheet: it sits where the
//         (max-height:479px) thumb is and leaves room for the on-screen keyboard
//                            the message field summons.
//                            ⚠ THE HEIGHT CLAUSE IS THE LANDSCAPE PHONE, and it is
//                            load-bearing rather than tidy. A phone on its side is
//                            844×390: too WIDE for the width clause, so it used to
//                            take the centred desktop dialog — which spends 48 px of
//                            `.sfb-wrap`'s padding on a box the keyboard has already
//                            cut to ~200. Measured in Chrome at 844×390 with the
//                            wrapper pinned to a 200 px visual viewport: the panel
//                            bottoms out at 176 while `.sfb-foot` runs to 205, so
//                            `.sfb-dialog`'s `overflow: hidden` cut the Send button
//                            in half; at 160 the button fell outside the panel
//                            entirely and `elementFromPoint` over it returned the
//                            host page. With this clause the same viewports give a
//                            full-width sheet with no wrapper padding, and Send is
//                            whole at 200 and still half-reachable at 160.
//                            ⚠ There is a floor under this and the clause does not
//                            remove it. `.sfb-body` cannot shrink past its own 32 px
//                            of padding, so the panel has a minimum of ~181 px; a
//                            visual viewport under ~196 px still clips the foot. Two
//                            fixed-height children and a scrolling one cannot do
//                            better without a scroller around the whole panel, which
//                            `place-items: end center` would then cut the top off.
//   max-height: …%           on the sheet, and `min(84vh, 100%)` on the centred
//                            dialog, so the panel is bounded by THE WRAPPER rather
//                            than by the viewport unit the wrapper has stopped
//                            agreeing with. `92%` also keeps the strip of backdrop
//                            that click-outside-to-close is reached through: a
//                            full-height sheet has no outside left to click.
//                            ⚠ `84vh` is declared first and then overwritten by the
//                            `min()`, for the same reason `inset: 0` is left under
//                            `100dvh`: a browser that cannot parse `min()` drops that
//                            declaration whole, and a dropped `max-height` is not the
//                            old behaviour but NO bound at all — the panel grows past
//                            the viewport and Send goes off the bottom, which is the
//                            bug this file is fixing. vite.widget.config.ts puts the
//                            floor at Firefox 72 (es2020); CSS `min()` is Firefox 75.
//   .sfb-head/.sfb-foot      `flex: none`, because flex shrinkage is proportional
//                            and would take its cut from all three children. On a
//                            viewport a keyboard has cut to ~300 px that is not a
//                            rounding difference: measured, the head loses about a
//                            third of its height and the foot goes with it, so the
//                            padding collapses and the controls overlap the rules
//                            above and below them. The body is the only part that
//                            may lose height, and it is the only part that scrolls.
//                            ⚠ `.sfb-body` carries `min-height: 0` and NOT a `flex`
//                            of its own. The grow half of `flex: 1 1 auto` cannot
//                            ever fire here: `.sfb-dialog` is a grid item under
//                            `place-items: center` / `end center`, so it is never
//                            stretched, its height is always its content clamped by
//                            `max-height`, and a flex container sized to its content
//                            has no free space to distribute. Shrink is the only
//                            direction that happens, and `flex-shrink: 1` is already
//                            the initial value — `min-height: 0` is the whole of what
//                            this line has to say. Measured both ways at 375×812 and
//                            at a 470 px and a 380 px visual viewport: every box
//                            identical to the tenth of a pixel.
//   .sfb-kind[aria-pressed]  selection carries a check glyph as well as the accent
//                            fill, so it survives greyscale and a red-green
//                            deficiency (WCAG 1.4.1). ⚠ The attribute is
//                            `aria-pressed` and must stay in step with dialog.ts:
//                            these buttons are a group with pressed state, not a
//                            radiogroup, and selecting on `aria-checked` here
//                            leaves the choice with no visible signal at all.
//   .sfb-honey               the honeypot: named plausibly, invisible to people,
//                            reachable by a bot that fills every field it finds,
//                            and hidden from assistive technology so nobody is
//                            ever asked to fill it in.
//
// Nothing here can leak out (shadow root) and nothing of the host's can leak in
// (`all: initial` on the host element, plus explicit values on every element that
// matters), which is what lets the panel keep its measured contrast ratios on a
// page whose background it cannot know.

export const WIDGET_CSS = `
:host {
  all: initial;
  --sfb-surface: #FFFFFF;
  --sfb-surface-2: #F4F6F7;
  --sfb-ink: #17191C;
  --sfb-muted: #5A6169;
  --sfb-hairline: #DDE1E4;
  --sfb-hairline-strong: #9AA2A9;
  --sfb-accent: #17514B;
  --sfb-accent-ink: #FFFFFF;
  --sfb-accent-soft: #E6EFED;
  --sfb-accent-line: #C7DBD7;
  --sfb-danger: #A0321F;
  --sfb-danger-soft: #FBEDEA;
  --sfb-danger-line: #EFCFC7;
  --sfb-warn: #7A4C00;
  --sfb-warn-soft: #FBF2E2;
  --sfb-warn-line: #E9D6AE;
  --sfb-radius: 12px;
  --sfb-radius-sm: 8px;
  --sfb-shadow: 0 12px 32px -10px rgba(16,22,28,.30), 0 2px 6px rgba(16,22,28,.10);
  --sfb-font: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
  --sfb-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
:host > * { font-family: var(--sfb-font); line-height: 1.45; }
* { box-sizing: border-box; }
button, input, textarea { font: inherit; color: inherit; margin: 0; }
:focus-visible { outline: 2px solid var(--sfb-accent); outline-offset: 2px; }

/* ---- launcher ---- */
.sfb-launcher {
  position: fixed;
  z-index: 2147483000;
  display: inline-flex;
  align-items: center;
  gap: 9px;
  height: 48px;
  width: 48px;
  padding: 0;
  justify-content: center;
  border: 1px solid var(--sfb-hairline);
  background: var(--sfb-surface);
  color: var(--sfb-accent);
  border-radius: 50%;
  box-shadow: var(--sfb-shadow);
  cursor: pointer;
  transition: width .16s ease, border-radius .16s ease, padding .16s ease, background .12s ease;
}
.sfb-launcher .sfb-launcher-label {
  display: none;
  font-size: 14px;
  font-weight: 600;
  color: var(--sfb-ink);
  white-space: nowrap;
}
.sfb-launcher:hover, .sfb-launcher:focus-visible {
  width: auto;
  padding: 0 16px 0 14px;
  border-radius: 24px;
  border-color: var(--sfb-hairline-strong);
}
.sfb-launcher:hover .sfb-launcher-label, .sfb-launcher:focus-visible .sfb-launcher-label { display: inline; }
.sfb-launcher:active { background: var(--sfb-surface-2); box-shadow: none; transform: scale(.96); }
.sfb-pos-bottom-right { right: 16px; bottom: calc(16px + env(safe-area-inset-bottom, 0px)); }
.sfb-pos-bottom-left  { left: 16px;  bottom: calc(16px + env(safe-area-inset-bottom, 0px)); }
.sfb-pos-top-right    { right: 16px; top: calc(16px + env(safe-area-inset-top, 0px)); }
.sfb-pos-top-left     { left: 16px;  top: calc(16px + env(safe-area-inset-top, 0px)); }

/* ---- dialog ---- */
.sfb-wrap {
  position: fixed;
  inset: 0;
  height: 100dvh;
  z-index: 2147483000;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(12,16,20,.44);
}
.sfb-dialog {
  width: 460px;
  max-width: 100%;
  max-height: 84vh;
  max-height: min(84vh, 100%);
  display: flex;
  flex-direction: column;
  background: var(--sfb-surface);
  color: var(--sfb-ink);
  border: 1px solid var(--sfb-hairline);
  border-radius: var(--sfb-radius);
  box-shadow: var(--sfb-shadow);
  overflow: hidden;
  animation: sfb-fade .18s ease;
}
@media (max-width: 479px), (max-height: 479px) {
  .sfb-wrap { place-items: end center; padding: 0; }
  .sfb-dialog {
    width: 100%;
    max-height: 92%;
    border: none;
    border-radius: var(--sfb-radius) var(--sfb-radius) 0 0;
    padding-bottom: env(safe-area-inset-bottom, 0px);
    animation: sfb-sheet .22s ease;
  }
}
@keyframes sfb-fade { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: none; } }
@keyframes sfb-sheet { from { transform: translateY(100%); } to { transform: none; } }
@keyframes sfb-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) {
  .sfb-dialog, .sfb-launcher { animation: none; transition: none; }
}

.sfb-head { flex: none; display: flex; align-items: center; gap: 12px; padding: 16px 16px 14px; border-bottom: 1px solid var(--sfb-hairline); }
.sfb-head-mark { display: grid; place-items: center; height: 32px; width: 32px; flex: none; border-radius: var(--sfb-radius-sm); background: var(--sfb-accent-soft); color: var(--sfb-accent); }
.sfb-title { font-size: 16px; font-weight: 600; letter-spacing: -.01em; }
.sfb-sub { font-size: 12.5px; color: var(--sfb-muted); }
.sfb-iconbtn {
  flex: none; display: grid; place-items: center; height: 44px; width: 44px;
  margin: -6px -6px -6px 0; border: none; background: transparent;
  border-radius: var(--sfb-radius-sm); color: var(--sfb-muted); cursor: pointer;
}
.sfb-iconbtn:hover { background: var(--sfb-surface-2); }
.sfb-body { min-height: 0; padding: 16px; display: flex; flex-direction: column; gap: 16px; overflow: auto; }
.sfb-foot { flex: none; padding: 14px 16px; border-top: 1px solid var(--sfb-hairline); background: var(--sfb-surface); }

.sfb-label { display: block; font-size: 13px; font-weight: 600; margin-bottom: 6px; }
.sfb-textarea {
  display: block; width: 100%; resize: vertical; min-height: 96px;
  border: 1px solid var(--sfb-hairline-strong); background: var(--sfb-surface);
  border-radius: var(--sfb-radius-sm); padding: 11px 12px;
  font-size: 15px; line-height: 1.45; color: var(--sfb-ink);
}
.sfb-meta { display: flex; justify-content: space-between; gap: 10px; margin-top: 5px; }
.sfb-hint { font-size: 12px; color: var(--sfb-muted); }
.sfb-count { font-family: var(--sfb-mono); font-size: 11.5px; color: var(--sfb-muted); }

.sfb-kinds { display: flex; gap: 8px; flex-wrap: wrap; }
.sfb-kind {
  display: inline-flex; align-items: center; gap: 7px; min-height: 44px; padding: 0 14px;
  border: 1px solid var(--sfb-hairline-strong); background: var(--sfb-surface); color: var(--sfb-ink);
  border-radius: var(--sfb-radius-sm); font-size: 14px; font-weight: 600; white-space: nowrap; cursor: pointer;
}
.sfb-kind[aria-pressed="true"] { border-color: var(--sfb-accent); background: var(--sfb-accent-soft); color: var(--sfb-accent); }
.sfb-kind svg { display: none; }
.sfb-kind[aria-pressed="true"] svg { display: inline; }

.sfb-files { display: flex; flex-direction: column; gap: 8px; }
.sfb-chip {
  display: flex; align-items: center; gap: 10px;
  border: 1px solid var(--sfb-hairline); background: var(--sfb-surface-2);
  border-radius: var(--sfb-radius-sm); padding: 8px 8px 8px 11px;
}
.sfb-chip-name { font-size: 13px; font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sfb-chip-size { font-family: var(--sfb-mono); font-size: 11.5px; color: var(--sfb-muted); }
.sfb-chip-state { flex: none; display: inline-flex; align-items: center; gap: 6px; font-size: 11.5px; font-weight: 600; }
.sfb-bar { margin-top: 8px; height: 4px; border-radius: 999px; background: var(--sfb-surface-2); overflow: hidden; }
.sfb-bar > span { display: block; height: 100%; border-radius: 999px; background: var(--sfb-accent); transition: width .2s ease; }

.sfb-pick {
  display: flex; align-items: center; justify-content: center; gap: 9px;
  min-height: 48px; width: 100%; border: 1px dashed var(--sfb-hairline-strong);
  background: var(--sfb-surface); color: var(--sfb-ink); border-radius: var(--sfb-radius-sm);
  font-size: 14px; font-weight: 600; cursor: pointer;
}
.sfb-pick[disabled] { border-color: var(--sfb-hairline); background: var(--sfb-surface-2); color: var(--sfb-muted); cursor: not-allowed; }

.sfb-disclosure { border: 1px solid var(--sfb-hairline); background: var(--sfb-surface-2); border-radius: var(--sfb-radius-sm); overflow: hidden; }
.sfb-disclosure-head { display: flex; align-items: flex-start; gap: 10px; padding: 11px 12px; }
.sfb-disclosure-body { border-top: 1px solid var(--sfb-hairline); background: var(--sfb-surface); padding: 12px; }
.sfb-link {
  display: inline-flex; align-items: center; gap: 6px; min-height: 32px; padding: 0;
  border: none; background: transparent; color: var(--sfb-accent);
  font-size: 12.5px; font-weight: 600; text-decoration: underline; cursor: pointer;
}
.sfb-kv { display: flex; gap: 10px; align-items: baseline; }
.sfb-kv > span:first-child { flex: none; width: 96px; font-size: 12px; color: var(--sfb-muted); }
.sfb-kv > span:last-child { flex: 1; min-width: 0; font-family: var(--sfb-mono); font-size: 11.5px; word-break: break-all; }
.sfb-console { border: 1px solid var(--sfb-hairline); border-radius: var(--sfb-radius-sm); overflow: hidden; margin-top: 12px; }
.sfb-console-head { padding: 9px 11px; border-bottom: 1px solid var(--sfb-hairline); background: var(--sfb-surface-2); }
.sfb-console-lines { padding: 9px 11px; display: flex; flex-direction: column; gap: 3px; max-height: 118px; overflow: auto; }
.sfb-console-lines > div { font-family: var(--sfb-mono); font-size: 11px; line-height: 1.5; color: var(--sfb-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sfb-optout { display: flex; align-items: center; gap: 10px; min-height: 44px; padding: 0 11px; border-top: 1px solid var(--sfb-hairline); cursor: pointer; font-size: 12.5px; font-weight: 600; }
.sfb-optout input { width: 18px; height: 18px; accent-color: var(--sfb-accent); }

.sfb-btn {
  display: flex; align-items: center; justify-content: center; gap: 9px;
  min-height: 48px; width: 100%; border: 1px solid var(--sfb-accent);
  background: var(--sfb-accent); color: var(--sfb-accent-ink);
  border-radius: var(--sfb-radius-sm); font-size: 15px; font-weight: 600; cursor: pointer;
}
.sfb-btn[disabled] { opacity: .65; cursor: default; }
.sfb-btn-quiet { background: var(--sfb-surface); color: var(--sfb-ink); border-color: var(--sfb-hairline-strong); }
.sfb-btn-row { display: flex; gap: 8px; }
.sfb-btn-row .sfb-btn-quiet { width: auto; flex: none; padding: 0 14px; }

.sfb-alert { display: flex; align-items: flex-start; gap: 10px; border-radius: var(--sfb-radius-sm); padding: 11px 12px; border: 1px solid; }
.sfb-alert-title { font-size: 13px; font-weight: 600; margin-bottom: 3px; }
.sfb-alert-body { font-size: 12.5px; line-height: 1.5; color: var(--sfb-ink); }
.sfb-tone-danger { border-color: var(--sfb-danger-line); background: var(--sfb-danger-soft); color: var(--sfb-danger); }
.sfb-tone-warn { border-color: var(--sfb-warn-line); background: var(--sfb-warn-soft); color: var(--sfb-warn); }
.sfb-tone-accent { border-color: var(--sfb-accent-line); background: var(--sfb-accent-soft); color: var(--sfb-accent); }

.sfb-status { display: flex; align-items: center; gap: 9px; font-size: 13.5px; font-weight: 600; }
.sfb-spinner { height: 16px; width: 16px; flex: none; border: 2px solid var(--sfb-accent); border-right-color: transparent; border-radius: 50%; display: inline-block; animation: sfb-spin .8s linear infinite; }
.sfb-quote { border: 1px solid var(--sfb-hairline); background: var(--sfb-surface-2); border-radius: var(--sfb-radius-sm); padding: 11px 12px; }
.sfb-quote-label { font-size: 12px; font-weight: 600; color: var(--sfb-muted); margin-bottom: 3px; }
.sfb-quote-text { font-size: 13px; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }

.sfb-success { padding: 24px 16px 16px; text-align: center; overflow: auto; }
.sfb-success-mark { margin: 0 auto 14px; display: grid; place-items: center; height: 52px; width: 52px; border-radius: 50%; background: var(--sfb-accent-soft); color: var(--sfb-accent); }
.sfb-success-title { font-size: 18px; font-weight: 600; margin-bottom: 6px; letter-spacing: -.01em; }
.sfb-success-body { margin: 0 auto 20px; max-width: 34ch; font-size: 13.5px; color: var(--sfb-muted); }
.sfb-ref { border: 1px solid var(--sfb-hairline); background: var(--sfb-surface-2); border-radius: var(--sfb-radius); padding: 16px; margin-bottom: 16px; }
.sfb-ref-label { font-size: 12px; font-weight: 600; letter-spacing: .06em; text-transform: uppercase; color: var(--sfb-muted); margin-bottom: 8px; }
.sfb-ref-code { font-family: var(--sfb-mono); font-size: 32px; font-weight: 600; letter-spacing: .10em; margin-bottom: 12px; }
.sfb-ref-copy { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 44px; padding: 0 16px; border: 1px solid var(--sfb-hairline-strong); background: var(--sfb-surface); color: var(--sfb-ink); border-radius: var(--sfb-radius-sm); font-size: 13.5px; font-weight: 600; cursor: pointer; }
.sfb-ref-help { margin: 0 auto 20px; max-width: 36ch; font-size: 12.5px; color: var(--sfb-muted); }

.sfb-honey { position: absolute; left: -9999px; width: 1px; height: 1px; opacity: 0; }
.sfb-sr { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
`
