import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent } from 'react'
import { ghostButton } from '@/components/ui'

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
  onError,
  onClose,
}: {
  url: string
  /** What the thumbnail's footer says — type and size. */
  label: string
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
  // Where to land after switching to actual size: the point that was clicked, as
  // a fraction of the image, and where it sat in the stage.
  const anchor = useRef<{ fx: number; fy: number; x: number; y: number } | null>(null)

  // ⚠ A layout effect, and no close() in a cleanup. Opening after paint flashes
  // the closed dialog in the card for a frame; closing from a cleanup would fire
  // `close` when StrictMode re-runs effects in dev, and shut the viewer the
  // moment it opened. Unmounting removes it from the top layer on its own.
  useLayoutEffect(() => {
    const d = dialogRef.current
    if (d && !d.open) d.showModal()
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
    const a = anchor.current ?? { fx: 0.5, fy: 0.5, x: stage.clientWidth / 2, y: stage.clientHeight / 2 }
    anchor.current = null
    stage.scrollLeft = img.offsetLeft + a.fx * img.offsetWidth - a.x
    stage.scrollTop = img.offsetTop + a.fy * img.offsetHeight - a.y
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

  const onImageClick = (e: MouseEvent<HTMLImageElement>) => {
    if (actual) {
      setActual(false)
      return
    }
    if (!zoomable) return
    const img = e.currentTarget.getBoundingClientRect()
    const stage = stageRef.current!.getBoundingClientRect()
    anchor.current = {
      fx: (e.clientX - img.left) / img.width,
      fy: (e.clientY - img.top) / img.height,
      x: e.clientX - stage.left,
      y: e.clientY - stage.top,
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
            <button onClick={() => setActual((a) => !a)} style={{ ...ghostButton, height: 32 }}>
              {actual ? 'Fit to screen' : 'Actual size'}
            </button>
          )}
          <button onClick={close} style={{ ...ghostButton, height: 32 }}>
            Close
          </button>
        </div>
        {/* The stage around the image is the backdrop: a click that lands on it,
            and not on the picture, closes the viewer. */}
        <div
          ref={stageRef}
          onClick={(e) => e.target === e.currentTarget && close()}
          style={{ position: 'relative', flex: 1, minHeight: 0, display: 'flex', overflow: 'auto', overscrollBehavior: 'contain', padding: actual ? 0 : 16 }}
        >
          {failed ? (
            <div style={{ margin: 'auto', maxWidth: 360, padding: 16, textAlign: 'center', fontSize: 13, color: 'oklch(0.9 0 0)' }}>
              Couldn't load the image. Its view link may have expired — reload the page to mint a fresh one.
            </div>
          ) : (
            <img
              ref={imgRef}
              src={url}
              alt="Attachment from the reporter, full size"
              onLoad={(e) => {
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
        </div>
      </div>
    </dialog>
  )
}
