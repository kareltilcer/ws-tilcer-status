import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent } from 'react'
import { Spinner, ghostButton } from '@/components/ui'

/** How far an arrow key pans the image at actual size, in CSS pixels. */
const PAN_KEYS: Partial<Record<string, [number, number]>> = {
  ArrowLeft: [-48, 0],
  ArrowRight: [48, 0],
  ArrowUp: [0, -48],
  ArrowDown: [0, 48],
}

/**
 * ImageLightbox shows one image attachment at full resolution, over the page.
 *
 * A native modal <dialog>, not the div overlay ConfirmDialog and KeyModal use:
 * `showModal()` makes the page behind it inert, keeps Tab inside, closes on
 * Escape and hands focus back to the thumbnail that opened it. A viewer is
 * opened and dismissed over and over, so those are the parts it has to get
 * right, and a div overlay does none of them.
 *
 * ⚠ In-page, never a new tab. The URL is a presigned bearer token (see
 * `attachmentUrl`), and a tab puts it in the address bar, the history and the
 * copy-link menu — the shareable places the endpoint's contract keeps it out of.
 */
export function ImageLightbox({
  url,
  label,
  retrying,
  onError,
  onClose,
}: {
  url: string
  /** What the thumbnail's footer says — type and size. */
  label: string
  /** The card is fetching the fresh view link `onError` asked for. */
  retrying: boolean
  /** The card's re-mint. The same URL the thumbnail loaded can have expired by
   *  the time this opens; a fresh one arrives through `url`. */
  onError: () => void
  onClose: () => void
}) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const imgRef = useRef<HTMLImageElement>(null)
  const [actual, setActual] = useState(false)
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null)
  // How far fit-to-screen shrank the image. 1 means it already fits, and then
  // actual size would look identical, so the toggle is not offered.
  const [fitScale, setFitScale] = useState(1)
  // Keyed to the URL rather than a flag, so a re-minted URL retries on its own.
  const [failedUrl, setFailedUrl] = useState<string | null>(null)
  const failed = failedUrl === url
  // Keyed the same way: a re-minted URL is a new download.
  const [loadedUrl, setLoadedUrl] = useState<string | null>(null)
  // ⚠ A failed link waits on the card's re-mint before it says anything. The
  // expiry message used to render in the same frame that asked for the fresh
  // link, and told the reader to reload a page that was already recovering.
  const loading = failed ? retrying : loadedUrl !== url
  // Where to land after switching to actual size: the point that was clicked, as
  // a fraction of the image, and where it was on screen.
  const anchor = useRef<{ fx: number; fy: number; cx: number; cy: number } | null>(null)
  const openedAt = useRef(0)
  // The press behind the next click in the stage: what it went down on, where,
  // how far the stage was scrolled then, and whether the mouse has since moved.
  const press = useRef<{ id: number; target: EventTarget; x: number; y: number; left: number; top: number; moved: boolean } | null>(null)

  // ⚠ A layout effect, declared before the one that measures, and no close() in
  // a cleanup. The measurement reads the image's laid-out width, which is 0 while
  // the dialog is still closed (display: none): a cached image measured then read
  // "0%" and offered a zoom it did not need. Closing from a cleanup would fire
  // `close` when StrictMode re-runs effects in dev, and shut the viewer the
  // moment it opened. Unmounting removes it from the top layer on its own.
  useLayoutEffect(() => {
    const d = dialogRef.current
    if (d && !d.open) {
      d.showModal()
      openedAt.current = performance.now()
    }
  }, [])

  const measure = () => {
    const img = imgRef.current
    if (img && img.naturalWidth > 0) setFitScale(Math.min(1, img.clientWidth / img.naturalWidth))
  }

  useLayoutEffect(() => {
    if (!actual) {
      measure()
      return
    }
    const stage = stageRef.current
    const img = imgRef.current
    if (!stage || !img) return
    const a = anchor.current
    anchor.current = null
    // ⚠ Where the stage is, read now rather than at the click. The switch
    // re-renders the toolbar with it — another percentage, another button label —
    // and at phone widths one line more or less of wrapped meta moves the stage
    // by that line, and the clicked point with it.
    const s = stage.getBoundingClientRect()
    const x = a ? a.cx - s.left : stage.clientWidth / 2
    const y = a ? a.cy - s.top : stage.clientHeight / 2
    stage.scrollLeft = img.offsetLeft + (a?.fx ?? 0.5) * img.offsetWidth - x
    stage.scrollTop = img.offsetTop + (a?.fy ?? 0.5) * img.offsetHeight - y
  }, [actual])

  // The image itself, not the window: anything that resizes the stage — the
  // window, the toolbar wrapping onto a second line — resizes a fitted image.
  useEffect(() => {
    const img = imgRef.current
    if (actual || !img) return
    const ro = new ResizeObserver(measure)
    ro.observe(img)
    return () => ro.disconnect()
  }, [actual, failed])

  // ⚠ onClose is called here, not left to the `close` event. That event is
  // queued, and Chrome holds it for as long as the page is not rendering: a
  // viewer shut from a background tab stayed mounted — closed, but with the
  // card still "viewing" — and the thumbnail then opened nothing, because
  // setting the flag it already held re-rendered nothing. close() itself is
  // synchronous, and it is what hands focus back to the thumbnail. Escape
  // still arrives through the event, the one close path this does not own.
  const close = () => {
    dialogRef.current?.close()
    onClose()
  }
  const zoomable = fitScale < 0.995

  // ⚠ Not the rest of a double-click. The click that opens the viewer is often
  // the first of two — a double-click on the thumbnail, a double-tap — and the
  // second landed on whatever the viewer had just put under the pointer: the
  // backdrop, which shut the viewer as it opened, or the picture, which jumped
  // to actual size at a point nobody chose. `detail` counts the clicks of one
  // gesture, which also makes a double-click on the picture one zoom, not two;
  // iOS reports every tap as the first, hence the time since opening as well.
  // The toolbar is under the pointer too: at phone widths a thumbnail near the
  // top of the screen sits right where Close and Actual size appear.
  const early = (e: MouseEvent) => e.detail > 1 || performance.now() - openedAt.current < 400
  // ⚠ Nor the end of a drag. A click goes to wherever the button came back up —
  // the common ancestor, when that is not where it went down — so a press on the
  // picture released beside it reached the backdrop and shut the viewer, and a
  // drag to pan at actual size ended as a click on the picture and dropped back
  // to fit. A click counts only on what was pressed, by a mouse that did not
  // travel. Touch needs no distance: a finger that moves is scrolling, and the
  // browser sends no click at all.
  const stray = (e: MouseEvent) => {
    const p = press.current
    return early(e) || !p || p.target !== e.target || p.moved
  }

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    const s = e.currentTarget
    const r = s.getBoundingClientRect()
    // A press on the stage's own scrollbar is the browser's: it is neither a pan
    // nor, whatever its click does, a click on the backdrop.
    const onScrollbar = e.target === s && (e.clientX - r.left >= s.clientLeft + s.clientWidth || e.clientY - r.top >= s.clientTop + s.clientHeight)
    press.current = onScrollbar ? null : { id: e.pointerId, target: e.target, x: e.clientX, y: e.clientY, left: s.scrollLeft, top: s.scrollTop, moved: false }
  }
  // At actual size a mouse drag pans, the way a desktop reader expects it to;
  // touch already pans natively. Captured once it is a drag, and not before: a
  // captured press is released on the stage, so its click would land there too.
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const p = press.current
    if (!p || p.id !== e.pointerId || e.pointerType !== 'mouse' || !(e.buttons & 1)) return
    const dx = e.clientX - p.x
    const dy = e.clientY - p.y
    if (!p.moved) {
      if (Math.hypot(dx, dy) < 5) return
      p.moved = true
      if (actual) e.currentTarget.setPointerCapture(e.pointerId)
    }
    if (actual) {
      e.currentTarget.scrollLeft = p.left - dx
      e.currentTarget.scrollTop = p.top - dy
    }
  }
  // And the keyboard's way to pan. Focus stays on a toolbar button, where arrow
  // keys do nothing, and the stage is a scroller only some browsers let Tab reach.
  const onKeyDown = (e: KeyboardEvent<HTMLDialogElement>) => {
    const stage = stageRef.current
    const step = PAN_KEYS[e.key]
    if (!actual || !stage || !step) return
    e.preventDefault()
    stage.scrollBy(step[0], step[1])
  }

  const onImageClick = (e: MouseEvent<HTMLImageElement>) => {
    if (stray(e)) return
    if (actual) {
      setActual(false)
      return
    }
    if (!zoomable) return
    const img = e.currentTarget.getBoundingClientRect()
    anchor.current = {
      fx: (e.clientX - img.left) / img.width,
      fy: (e.clientY - img.top) / img.height,
      cx: e.clientX,
      cy: e.clientY,
    }
    setActual(true)
  }

  const meta = [label, natural && `${natural.w} × ${natural.h}`, natural && `${actual ? 100 : Math.round(fitScale * 100)}%`]
    .filter(Boolean)
    .join(' · ')

  return (
    <dialog
      ref={dialogRef}
      onClose={onClose}
      onKeyDown={onKeyDown}
      aria-label={`Attachment — ${label}`}
      style={{ inset: 0, width: '100%', height: '100%', maxWidth: '100%', maxHeight: '100%', margin: 0, padding: 0, border: 'none', background: 'oklch(0 0 0 / .86)', color: 'var(--text)', overflow: 'hidden' }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        <div style={{ flex: 'none', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', padding: '10px 14px', background: 'var(--s1)', borderBottom: '1px solid var(--border)' }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13.5, fontWeight: 700 }}>Attachment</div>
            <div style={{ fontFamily: 'var(--mono)', fontSize: 11, color: 'var(--subtle)' }}>{meta}</div>
          </div>
          {!failed && (zoomable || actual) && (
            <button onClick={(e) => !early(e) && setActual((a) => !a)} style={{ ...ghostButton, height: 32 }}>
              {actual ? 'Fit to screen' : 'Actual size'}
            </button>
          )}
          <button onClick={(e) => !early(e) && close()} style={{ ...ghostButton, height: 32 }}>
            Close
          </button>
        </div>
        {/* The stage around the image is the backdrop: a click that lands on it,
            and not on the picture, closes the viewer. */}
        <div
          ref={stageRef}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onClick={(e) => e.target === e.currentTarget && !stray(e) && close()}
          style={{ position: 'relative', flex: 1, minHeight: 0, display: 'flex', overflow: 'auto', overscrollBehavior: 'contain', padding: actual ? 0 : 16 }}
        >
          {failed ? (
            !retrying && (
              <div style={{ margin: 'auto', maxWidth: 360, padding: 16, textAlign: 'center', fontSize: 13, color: 'oklch(0.9 0 0)' }}>
                Couldn't load the image. Its view link may have expired — reload the page to mint a fresh one.
              </div>
            )
          ) : (
            <img
              ref={imgRef}
              src={url}
              alt="Attachment from the reporter, full size"
              // ⚠ No native drag. A drag on the picture is the stage's pan, and a
              // native one, dropped on the tab strip, opens the view URL — the
              // bearer token this viewer is in-page to keep out of a tab.
              draggable={false}
              onLoad={(e) => {
                setLoadedUrl(url)
                setNatural({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })
                if (!actual) measure()
              }}
              onError={() => {
                setFailedUrl(url)
                onError()
              }}
              onClick={onImageClick}
              style={{
                display: 'block',
                flex: 'none',
                // Auto margins in a flex container centre the image while it is
                // smaller than the stage, and — unlike centring alignment — never
                // push its top-left corner out of scroll reach once it is larger.
                margin: 'auto',
                maxWidth: actual ? 'none' : '100%',
                maxHeight: actual ? 'none' : '100%',
                cursor: actual ? 'zoom-out' : zoomable ? 'zoom-in' : 'default',
              }}
            />
          )}
          {/* A screenshot is up to 10 MB, and the viewer can open before the
              thumbnail has finished fetching it: the stage was empty and dark
              until it arrived. Faded in late, so an image already in memory —
              the usual case — never shows it. Clicks pass through to the stage. */}
          {loading && (
            <div role="status" style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', pointerEvents: 'none' }}>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, padding: '7px 12px', borderRadius: 8, background: 'oklch(0 0 0 / .6)', color: 'oklch(0.9 0 0)', fontSize: 12.5, animation: 'om-fadein .2s ease .25s both' }}>
                <Spinner size={13} />
                {failed ? 'Fetching a fresh view link…' : 'Loading…'}
              </span>
            </div>
          )}
        </div>
      </div>
    </dialog>
  )
}
