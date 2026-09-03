import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import * as api from '@/api/endpoints'
import { qk } from '@/api/keys'
import { paths } from '@/app/routes'
import { useAuth } from '@/app/auth'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { AttachmentStateLabel, KindChip, StateBlock, StateChip, cardStyle, ghostButton, primaryButton } from '@/components/ui'
import { useMediaQuery } from '@/lib/useMediaQuery'
import { fileSize, relativeTime } from '@/lib/format'
import type { AttachmentSummary, Report, ReportState } from '@/api/types'

const TRIAGE: { state: ReportState; note: string }[] = [
  { state: 'new', note: 'Unread. The only state that counts toward the board badge.' },
  { state: 'open', note: 'Seen, not decided. Clears the badge without judging it.' },
  { state: 'resolved', note: 'Done. Stamps resolved_at.' },
  { state: 'declined', note: 'Not doing it. Nothing goes back to the reporter.' },
]

export function ReportDetail() {
  const { ref } = useParams()
  const nav = useNavigate()
  const qc = useQueryClient()
  const { isAdmin } = useAuth()
  const isMobile = useMediaQuery('(max-width: 900px)')
  const [confirmDelete, setConfirmDelete] = useState(false)

  const q = useQuery({ queryKey: qk.report(ref!), queryFn: () => api.getReport(ref!), enabled: !!ref })
  const report = q.data

  // ⚠ `stored` only. A `missing` row was swept and its object is already gone; a
  // `pending` row never landed one. Counting every row made the delete
  // confirmation — the one text in this app that has to be exact, because the
  // action is irreversible — claim it was about to destroy files that were not
  // there, and on a text-only report (the normal case, per the attachments card's
  // own copy) it offered to remove "the 0 stored files".
  const storedFiles = (report?.attachments ?? []).filter((a) => a.state === 'stored').length

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: qk.report(ref!) })
    // `new` is what drives the board badge, so any triage invalidates the board.
    void qc.invalidateQueries({ queryKey: ['reports'] })
    void qc.invalidateQueries({ queryKey: qk.sites() })
  }

  const triage = useMutation({
    mutationFn: (state: ReportState) => api.triageReport(ref!, { state }),
    onSuccess: (updated) => {
      invalidate()
      toast.success(`${updated.ref} moved to ${updated.state}`)
    },
    onError: () => toast.error('Could not change the state'),
  })

  const saveNote = useMutation({
    // Trimmed on the way out, so what comes back is what the dirty check below
    // compares against — a note of nothing but spaces is null, once.
    mutationFn: (note: string) => api.triageReport(ref!, { internal_note: note.trim() || null }),
    onSuccess: () => {
      invalidate()
      toast.success('Note saved')
    },
    onError: () => toast.error('Could not save the note'),
  })

  const del = useMutation({
    mutationFn: () => api.deleteReport(ref!),
    onSuccess: () => {
      // ⚠ Removed, not invalidated. Invalidating refetches the report this
      // component is still mounted on, so the 404 that answers can paint the
      // "no report with that reference" block on the way out.
      qc.removeQueries({ queryKey: qk.report(ref!) })
      void qc.invalidateQueries({ queryKey: ['reports'] })
      void qc.invalidateQueries({ queryKey: qk.sites() })
      toast.success(storedFiles > 0 ? 'Report deleted — stored files removed' : 'Report deleted')
      nav(paths.reports)
    },
    onError: () => toast.error('Could not delete the report'),
  })

  const back = (
    <button
      onClick={() => nav(paths.reports)}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 5,
        background: 'none',
        border: 'none',
        color: 'var(--muted)',
        fontSize: 13,
        fontWeight: 600,
        cursor: 'pointer',
        padding: 0,
        marginBottom: 14,
        fontFamily: 'inherit',
      }}
    >
      ‹ Inbox
    </button>
  )

  if (q.isLoading) {
    return (
      <div>
        {back}
        <div className="om-skel" style={{ height: 26, width: 180, marginBottom: 14 }} />
        <div className="om-skel" style={{ height: 13, width: '42%', marginBottom: 24 }} />
        <div className="om-skel" style={{ height: 120, width: '100%', marginBottom: 12 }} />
        <div className="om-skel" style={{ height: 180, width: '100%' }} />
      </div>
    )
  }

  // ⚠ `q.isError` is deliberately NOT in this condition. A failed BACKGROUND
  // refetch — and every triage click invalidates this key, so there is one after
  // each of them — sets `isError` while `data` still holds the report that is on
  // screen. Reading it here replaced a report that exists, and had just been
  // patched successfully, with "no report with that reference … it may have been
  // deleted". Absent data is the only thing this block can honestly claim.
  if (!report) {
    return (
      <div>
        {back}
        <StateBlock
          icon="⚠"
          danger
          title="No report with that reference"
          body={
            <>
              <code style={{ fontFamily: 'var(--mono)' }}>{ref}</code> doesn't match a stored report. It may have been
              deleted, or misheard — the alphabet has no I, L, O or U, so a <b style={{ color: 'var(--text)' }}>0</b> is
              never an <b style={{ color: 'var(--text)' }}>O</b>.
            </>
          }
        >
          <button onClick={() => nav(paths.reports)} style={ghostButton}>
            Back to inbox
          </button>
        </StateBlock>
      </div>
    )
  }

  return (
    <div>
      {back}
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap', marginBottom: 20 }}>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 11, flexWrap: 'wrap', marginBottom: 7 }}>
            <h1 style={{ margin: 0, fontFamily: 'var(--mono)', fontSize: 22, fontWeight: 600, letterSpacing: '.06em' }}>{report.ref}</h1>
            <StateChip state={report.state} />
            <KindChip kind={report.kind} />
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', fontSize: 12.5, color: 'var(--muted)' }}>
            <span style={{ fontFamily: 'var(--mono)', color: 'var(--subtle)' }}>{report.site_id}</span>
            <span style={{ opacity: 0.4 }}>·</span>
            <span>{report.reporter_label ?? 'no name given'}</span>
            <span style={{ opacity: 0.4 }}>·</span>
            <span>{relativeTime(report.created_at)}</span>
          </div>
        </div>
        {isAdmin && (
          <button
            onClick={() => setConfirmDelete(true)}
            style={{ ...ghostButton, background: 'transparent', color: 'var(--danger-text)', border: '1px solid color-mix(in oklab, var(--danger) 45%, transparent)' }}
          >
            Delete
          </button>
        )}
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: isMobile ? '1fr' : 'minmax(0, 1fr) 320px', gap: 16 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16, minWidth: 0 }}>
          <div style={cardStyle}>
            <h2 style={{ margin: '0 0 12px', fontSize: 15, fontWeight: 700 }}>Message</h2>
            <p style={{ margin: 0, fontSize: 14.5, lineHeight: 1.6, whiteSpace: 'pre-wrap' }}>{report.message}</p>
            <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--border)', fontSize: 12, color: 'var(--subtle)' }}>
              {report.locale ? `Written in ${report.locale} · ` : ''}the name is a label the host app supplied, not a verified identity.
            </div>
          </div>

          <Attachments report={report} />
          <Context report={report} />
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 16, minWidth: 0 }}>
          <div style={cardStyle}>
            <h2 style={{ margin: '0 0 12px', fontSize: 15, fontWeight: 700 }}>Triage</h2>
            {/* ⚠ A group of buttons, not a radiogroup. Each one is a server
                action rather than a form choice, and `role="radio"` would
                promise a keyboard user arrow-key navigation and a single tab
                stop that plain buttons neither have nor need. `aria-pressed`
                is the same signal without the promise — the pattern the board's
                filter chips already use. */}
            <div role="group" aria-label="Report state" style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
              {TRIAGE.map((t) => {
                const on = report.state === t.state
                return (
                  <button
                    key={t.state}
                    aria-pressed={on}
                    disabled={!isAdmin || triage.isPending}
                    onClick={() => !on && triage.mutate(t.state)}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 11,
                      width: '100%',
                      textAlign: 'left',
                      padding: '10px 12px',
                      borderRadius: 9,
                      cursor: isAdmin ? 'pointer' : 'default',
                      fontFamily: 'inherit',
                      color: 'var(--text)',
                      border: `1px solid ${on ? 'color-mix(in oklab, var(--accent) 45%, transparent)' : 'var(--border)'}`,
                      background: on ? 'color-mix(in oklab, var(--accent) 10%, transparent)' : 'var(--s2)',
                    }}
                  >
                    <span style={{ flex: 1 }}>
                      <span style={{ display: 'block', fontSize: 13, fontWeight: 700, textTransform: 'capitalize' }}>{t.state}</span>
                      <span style={{ display: 'block', fontSize: 11.5, fontWeight: 500, color: 'var(--muted)' }}>{t.note}</span>
                    </span>
                    {on && <span aria-hidden style={{ color: 'var(--accent)' }}>✓</span>}
                  </button>
                )
              })}
            </div>
            <p style={{ margin: '12px 0 0', fontSize: 11.5, color: 'var(--subtle)' }}>
              Only <b style={{ color: 'var(--text)' }}>new</b> counts toward the board badge. Moving a report to open
              clears the badge without deciding anything.
            </p>
          </div>

          {/* ⚠ Hidden from a non-admin, not disabled for one. The server nulls
              `internal_note` for a session without the admin role, so the card
              rendered for an editor with an empty textarea — which reads as "no
              note has been written", not as "this is not yours to see". A note
              Karel wrote was shown to them as its own absence, and the screen
              would look identical if the redaction ever regressed. The Delete
              button above takes the same treatment. */}
          {isAdmin && (
            <InternalNote key={report.ref} report={report} pending={saveNote.isPending} onSave={(n) => saveNote.mutate(n)} />
          )}

          <div style={{ ...cardStyle, padding: '16px 18px' }}>
            <div style={{ fontSize: 12.5, fontWeight: 700, marginBottom: 8 }}>What the reporter sees</div>
            <p style={{ margin: 0, fontSize: 12, color: 'var(--muted)' }}>
              Nothing after sending. There is no reply channel, no status lookup and no notification — the reference
              code is the whole of their side of this.
            </p>
          </div>
        </div>
      </div>

      {confirmDelete && (
        <ConfirmDialog
          title="Delete this report?"
          body={
            storedFiles > 0
              ? `Deleting ${report.ref} removes the text, the context and the ${storedFiles} stored file${storedFiles === 1 ? '' : 's'}. This cannot be undone.`
              : `Deleting ${report.ref} removes the text and the context. This cannot be undone.`
          }
          confirmLabel="Delete report"
          danger
          onCancel={() => setConfirmDelete(false)}
          onConfirm={() => {
            setConfirmDelete(false)
            del.mutate()
          }}
        />
      )}
    </div>
  )
}

function Attachments({ report }: { report: Report }) {
  return (
    <div style={cardStyle}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap', marginBottom: 14 }}>
        <h2 style={{ margin: 0, fontSize: 15, fontWeight: 700 }}>
          Attachments <span style={{ fontWeight: 500, color: 'var(--subtle)', fontSize: 13 }}>· {report.attachments.length}</span>
        </h2>
        {/* ⚠ No number. The lifetime is STATUS_FEEDBACK_VIEW_TTL, which this
            screen does not read — naming a figure here made the page assert a
            setting the deployment might not be running. */}
        <span style={{ fontSize: 11.5, color: 'var(--subtle)' }}>View links are minted on demand and expire shortly after</span>
      </div>
      {report.attachments.length === 0 ? (
        <div style={{ display: 'grid', placeItems: 'center', minHeight: 88, border: '1px dashed var(--border)', borderRadius: 9, color: 'var(--muted)', fontSize: 13, textAlign: 'center', padding: 14 }}>
          Nothing attached. Most reports are text only — that is the normal case, not a missing step.
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(210px, 1fr))', gap: 12 }}>
          {report.attachments.map((a) => (
            <AttachmentCard key={a.id} reportRef={report.ref} attachment={a} />
          ))}
        </div>
      )}
    </div>
  )
}

/** viewUrlLifetime is what is left of a minted view URL, less a margin: the
 *  browser's clock and the signer's are not the same clock, and a link that is
 *  reused in its last seconds is one the bucket may already have stopped
 *  honouring. Zero for anything absent or already expired, which is React
 *  Query's "stale", so the next mount mints a fresh one. */
function viewUrlLifetime(expiresAt: string | undefined): number {
  const at = expiresAt ? Date.parse(expiresAt) : NaN
  return Number.isNaN(at) ? 0 : Math.max(0, at - Date.now() - 30_000)
}

function AttachmentCard({ reportRef, attachment }: { reportRef: string; attachment: AttachmentSummary }) {
  const stored = attachment.state === 'stored'
  const q = useQuery({
    queryKey: qk.attachmentUrl(reportRef, attachment.id),
    queryFn: () => api.attachmentUrl(reportRef, attachment.id),
    enabled: stored,
    // ⚠ How long the URL may be reused comes from the `expires_at` the same
    // response carries, not from a constant here. STATUS_FEEDBACK_VIEW_TTL is a
    // dial (the default is five minutes), and against a hardcoded four every
    // deployment that shortened it served a cached link for minutes after the
    // bucket had stopped honouring it — every image on the report broken, and
    // the one permitted re-mint spent on the first of them.
    staleTime: (q) => viewUrlLifetime(q.state.data?.expires_at),
    gcTime: 5 * 60_000,
    retry: false,
    // ⚠ Off, against the app-wide default. Coming back to the tab after four
    // minutes would otherwise re-mint every attachment's URL at once and swap
    // every <img src> on the page — reloading images that were on screen and
    // fine. It is the same churn keys.ts moved this key out of the report's
    // prefix to avoid; the window-focus default is the other way in.
    refetchOnWindowFocus: false,
  })
  // ⚠ The URL outlives nothing: it is a five-minute bearer token, and while this
  // card stays mounted nothing re-mints it. An admin who reads a report for six
  // minutes and then presses play sent range requests against an expired URL, R2
  // answered 403, and the player stopped with no message — `isError` is false,
  // because minting had succeeded. A poll is the wrong answer (it would swap
  // every <img src> on the page every few minutes, and reload a video mid-play),
  // so the media asks for a fresh one when it actually fails. Capped at one: an
  // object that is broken rather than expired must not spin the mint endpoint
  // against the service's single writer connection.
  const [reminted, setReminted] = useState(false)
  const remint = () => {
    if (reminted) return
    setReminted(true)
    void q.refetch()
  }
  const isVideo = attachment.content_type.startsWith('video/')
  const size = fileSize(attachment.byte_size)
  const shortType = attachment.content_type.split('/')[1]?.toUpperCase() ?? attachment.content_type

  return (
    <div style={{ border: '1px solid var(--border)', borderRadius: 10, overflow: 'hidden', background: 'var(--s2)' }}>
      <div style={{ height: 126, display: 'grid', placeItems: 'center', background: 'var(--s3)', overflow: 'hidden' }}>
        {!stored ? (
          // ⚠ A real, expected state — not an error. The object was never
          // uploaded, or the sweep collected it after the unclaimed TTL.
          <div style={{ textAlign: 'center', padding: 12 }}>
            <div aria-hidden style={{ fontSize: 20, color: 'var(--warn-text)' }}>⊘</div>
            <div style={{ fontFamily: 'var(--mono)', fontSize: 10.5, color: 'var(--subtle)', marginTop: 6 }}>
              {attachment.state === 'missing'
                ? 'file missing — never uploaded, or swept'
                : 'not confirmed yet — the claim has not arrived'}
            </div>
          </div>
        ) : q.isError ? (
          <div style={{ fontFamily: 'var(--mono)', fontSize: 10.5, color: 'var(--warn-text)', padding: 12, textAlign: 'center' }}>
            couldn't mint a view link
          </div>
        ) : !q.data ? (
          <div className="om-skel" style={{ height: '100%', width: '100%', borderRadius: 0 }} />
        ) : isVideo ? (
          <video src={q.data.url} onError={remint} controls preload="metadata" style={{ maxHeight: '100%', maxWidth: '100%' }} />
        ) : (
          <img src={q.data.url} onError={remint} alt="Attachment from the reporter" style={{ maxHeight: '100%', maxWidth: '100%', objectFit: 'contain' }} />
        )}
      </div>
      <div style={{ padding: '9px 11px', borderTop: '1px solid var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
        <span style={{ fontFamily: 'var(--mono)', fontSize: 11, color: 'var(--subtle)' }}>
          {shortType} · {size}
        </span>
        <AttachmentStateLabel state={attachment.state} />
      </div>
    </div>
  )
}

/**
 * webLink returns the page URL only when it is one the dashboard may hand to
 * window.open.
 *
 * ⚠ `page_url` is attacker-controlled. The widget key lives in the source of a
 * public host page, so anyone who reads it can post a report carrying whatever
 * this field will hold, and the server stores it as text with no scheme check.
 * `window.open` is not safe for a `javascript:` URL just because it is not an
 * <a href>: the URL is evaluated, and this dashboard is the one place a stored
 * one would run with an admin session behind it. http and https only.
 */
function webLink(pageUrl: string | null): string | null {
  if (!pageUrl) return null
  try {
    const parsed = new URL(pageUrl)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : null
  } catch {
    // Not a URL at all — a relative path, or something that was never one.
    return null
  }
}

function Context({ report }: { report: Report }) {
  const href = webLink(report.page_url)
  const rows: [string, string | null][] = [
    ['referrer', report.referrer],
    ['user agent', report.user_agent],
    ['viewport', report.viewport],
    ['locale', report.locale],
    ['release', report.app_release],
  ]
  const consoleTail = report.console_tail ?? []
  return (
    <div style={cardStyle}>
      <h2 style={{ margin: '0 0 4px', fontSize: 15, fontWeight: 700 }}>Context</h2>
      <p style={{ margin: '0 0 14px', fontSize: 12, color: 'var(--muted)' }}>
        Sent by the widget, shown as received. Nothing here was typed by the reporter.
      </p>
      <div style={{ border: '1px solid var(--border)', borderRadius: 9, overflow: 'hidden' }}>
        {report.page_url && (
          <>
            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12, padding: '11px 13px', background: 'var(--s2)', borderBottom: '1px solid var(--border)', flexWrap: 'wrap' }}>
              <span style={{ flex: 'none', width: 88, fontSize: 12, color: 'var(--subtle)', paddingTop: 2 }}>page url</span>
              <code style={{ flex: 1, minWidth: 200, fontFamily: 'var(--mono)', fontSize: 11.5, wordBreak: 'break-all' }}>{report.page_url}</code>
              {href ? (
                <button
                  onClick={() => window.open(href, '_blank', 'noopener,noreferrer')}
                  style={{ flex: 'none', display: 'inline-flex', alignItems: 'center', gap: 6, height: 28, padding: '0 10px', border: '1px solid var(--border-strong)', background: 'var(--s3)', color: 'var(--text)', borderRadius: 7, fontSize: 11.5, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit' }}
                >
                  Open
                </button>
              ) : (
                <span style={{ flex: 'none', fontSize: 11.5, color: 'var(--warn-text)' }}>not a web address</span>
              )}
            </div>
            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 9, padding: '10px 13px', borderBottom: '1px solid var(--border)', background: 'color-mix(in oklab, var(--warn) 8%, transparent)' }}>
              <span aria-hidden style={{ color: 'var(--warn-text)', flex: 'none', marginTop: 1 }}>⚠</span>
              <div style={{ fontSize: 11.5, color: 'var(--muted)' }}>
                Deliberately not a link. A host app can put a token in a query string, and a stray click would send it
                to whatever sits at the other end. <b style={{ color: 'var(--text)' }}>Open</b> is the explicit action.
              </div>
            </div>
          </>
        )}
        {rows.map(([k, v]) =>
          v ? (
            <div key={k} style={{ display: 'flex', alignItems: 'flex-start', gap: 12, padding: '9px 13px', borderBottom: '1px solid var(--border)' }}>
              <span style={{ flex: 'none', width: 88, fontSize: 12, color: 'var(--subtle)' }}>{k}</span>
              <span style={{ flex: 1, minWidth: 0, fontFamily: 'var(--mono)', fontSize: 11.5, wordBreak: 'break-word' }}>{v}</span>
            </div>
          ) : null,
        )}
      </div>

      {consoleTail.length > 0 || report.last_error ? (
        <div style={{ marginTop: 14 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap', marginBottom: 7 }}>
            <div style={{ fontSize: 12.5, fontWeight: 700 }}>
              Console tail{' '}
              <span style={{ fontWeight: 500, color: 'var(--subtle)' }}>· shown to the reporter before sending</span>
            </div>
            <span style={{ fontFamily: 'var(--mono)', fontSize: 11, color: 'var(--subtle)' }}>console_capture on</span>
          </div>
          {consoleTail.length > 0 && (
            <pre style={{ margin: '0 0 12px', fontFamily: 'var(--mono)', fontSize: 11, lineHeight: 1.7, background: 'var(--s2)', border: '1px solid var(--border)', borderRadius: 8, padding: 12, overflow: 'auto', color: 'var(--muted)', whiteSpace: 'pre' }}>
              {consoleTail.join('\n')}
            </pre>
          )}
          {report.last_error && (
            <>
              <div style={{ fontSize: 12, fontWeight: 700, marginBottom: 6 }}>Last JavaScript error</div>
              <pre style={{ margin: 0, fontFamily: 'var(--mono)', fontSize: 11, lineHeight: 1.7, background: 'var(--s2)', border: '1px solid color-mix(in oklab, var(--err-orange) 40%, transparent)', borderRadius: 8, padding: 12, overflow: 'auto', color: 'var(--err-orange-text)', whiteSpace: 'pre' }}>
                {report.last_error}
              </pre>
            </>
          )}
        </div>
      ) : (
        <div style={{ marginTop: 14, display: 'flex', alignItems: 'flex-start', gap: 11, padding: '12px 13px', border: '1px dashed var(--border)', borderRadius: 9, background: 'var(--s2)' }}>
          <span aria-hidden style={{ color: 'var(--subtle)', flex: 'none', marginTop: 1 }}>⏻</span>
          {/* ⚠ This says what is true — the report carries no console lines — and
              stops there. A report arrives empty for three different reasons and
              nothing in it tells them apart: capture is off for the site (the
              default), capture is on and the reporter unticked it before sending,
              or capture is on and the app logged nothing. Naming the first would
              send the reader to flip a switch that may already be on. */}
          <div style={{ fontSize: 12.5, color: 'var(--muted)' }}>
            No console output with this report. Either{' '}
            <code style={{ fontFamily: 'var(--mono)' }}>console_capture</code> is off for the site — it is off by
            default, and the site's feedback panel turns it on — or the reporter chose not to send the capture.
          </div>
        </div>
      )}
    </div>
  )
}

function InternalNote({
  report,
  pending,
  onSave,
}: {
  report: Report
  pending: boolean
  onSave: (note: string) => void
}) {
  // ⚠ Seeded once per report and never re-seeded from the server. The effect
  // that did it fired on `report.internal_note`, which is exactly what changes
  // when your own save comes back — so a word typed while the PATCH was in
  // flight was overwritten by the value that had been sent a second earlier,
  // silently. Switching reports resets it through the `key` at the call site,
  // which is the one case that has to reset.
  const [note, setNote] = useState(report.internal_note ?? '')

  return (
    <div style={cardStyle}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
        <h2 style={{ margin: 0, fontSize: 15, fontWeight: 700 }}>Internal note</h2>
        <span style={{ fontFamily: 'var(--mono)', fontSize: 10.5, padding: '2px 7px', borderRadius: 5, background: 'var(--s3)', color: 'var(--subtle)' }}>private</span>
      </div>
      <p style={{ margin: '0 0 10px', fontSize: 12, color: 'var(--muted)' }}>
        Never leaves the dashboard and never reaches the reporter.
      </p>
      <textarea
        rows={4}
        value={note}
        onChange={(e) => setNote(e.target.value)}
        style={{ display: 'block', width: '100%', resize: 'vertical', border: '1px solid var(--border)', background: 'var(--s2)', borderRadius: 8, padding: '10px 11px', fontFamily: 'inherit', fontSize: 13, lineHeight: 1.5, color: 'var(--text)', outline: 'none' }}
      />
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 10 }}>
        <button
          onClick={() => onSave(note)}
          // ⚠ Compared trimmed, because the mutation SENDS trimmed. On the raw
          // value, a note of nothing but spaces saves as null, comes back
          // unchanged, and leaves the button enabled on a no-op forever.
          disabled={pending || note.trim() === (report.internal_note ?? '')}
          style={{ ...primaryButton, height: 34 }}
        >
          Save note
        </button>
      </div>
    </div>
  )
}
