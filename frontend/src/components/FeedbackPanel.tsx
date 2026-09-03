import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import * as api from '@/api/endpoints'
import { ApiError } from '@/api/client'
import { qk } from '@/api/keys'
import { paths } from '@/app/routes'
import { useAuth } from '@/app/auth'
import { CopyableCode } from '@/components/CodeSnippet'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { KeyModal } from '@/components/KeyModal'
import { UnreadBadge, cardStyle, ghostButton } from '@/components/ui'
import { widgetEmbedSnippet } from '@/lib/snippets'
import { relativeTime } from '@/lib/format'

/**
 * FeedbackPanel is the site-detail half of v3: the kill switch, the console
 * capture flag, the widget key and the embed snippet.
 *
 * ⚠ A site with no configuration row reads as disabled rather than as missing —
 * absence is the default state, not something to repair (V3-D03) — so this panel
 * renders the same way for a site that has never been touched.
 */
export function FeedbackPanel({ siteId, openReports }: { siteId: string; openReports: number | null }) {
  const qc = useQueryClient()
  const nav = useNavigate()
  const { isAdmin } = useAuth()
  const [issuedKey, setIssuedKey] = useState<string | null>(null)
  const [confirmRotate, setConfirmRotate] = useState(false)

  const metaQ = useQuery({ queryKey: qk.meta(), queryFn: () => api.getMeta(), staleTime: 5 * 60_000 })
  // ⚠ The deployment-level switch, not the site's: with no object storage there
  // is nowhere to put an attachment, so enabling is a 503. The control says so
  // instead of failing on click.
  //
  // ⚠ Three states, not two. `metaQ.data === undefined` is "we do not know yet"
  // — it is the answer both while the request is in flight and forever after it
  // failed — and reading it as "configured" is how a deployment with no object
  // storage ends up offering the switch this block exists to withhold.
  const storageReady = metaQ.data?.feedback_enabled === true
  const storageUnknown = metaQ.data === undefined

  // Gated on the same condition that decides whether the configuration is ever
  // rendered. Without it a deployment with no object storage still asked for a
  // per-site config it would never show, on every site-detail load, against a
  // service with one writer connection.
  const cfgQ = useQuery({
    queryKey: qk.feedbackConfig(siteId),
    queryFn: () => api.getFeedbackConfig(siteId),
    enabled: storageReady,
  })
  const cfg = cfgQ.data

  const update = useMutation({
    mutationFn: (body: { enabled?: boolean; console_capture?: boolean }) => api.updateFeedbackConfig(siteId, body),
    onSuccess: (updated) => {
      void qc.invalidateQueries({ queryKey: qk.feedbackConfig(siteId) })
      void qc.invalidateQueries({ queryKey: qk.sites() })
      // The plaintext key comes back exactly once, on the first enable.
      if (updated.widget_key) setIssuedKey(updated.widget_key)
      else toast.success('Feedback settings saved')
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError && e.status === 503
          ? 'This deployment has no object storage configured'
          : e instanceof ApiError && e.detail
            ? e.detail
            : 'Could not save the feedback settings',
      ),
  })

  const rotate = useMutation({
    mutationFn: () => api.rotateWidgetKey(siteId),
    onSuccess: (r) => {
      setIssuedKey(r.widget_key)
      void qc.invalidateQueries({ queryKey: qk.feedbackConfig(siteId) })
    },
    onError: () => toast.error('Could not rotate the widget key'),
  })

  return (
    <div style={cardStyle}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 14, flexWrap: 'wrap', marginBottom: 4 }}>
        <div>
          <h2 style={{ margin: '0 0 3px', fontSize: 15, fontWeight: 700 }}>User feedback</h2>
          <p style={{ margin: 0, fontSize: 12.5, color: 'var(--muted)', maxWidth: '62ch' }}>
            The embedded widget lets people using this site write to you. Reports land in the inbox — they never affect
            this site's colour.
          </p>
        </div>
        {!!openReports && (
          <button
            onClick={() => nav(paths.reports)}
            style={{ border: 'none', background: 'none', padding: 0, cursor: 'pointer' }}
            aria-label={`${openReports} new reports — open the inbox`}
          >
            <UnreadBadge count={openReports} />
          </button>
        )}
      </div>

      {storageUnknown && metaQ.isError ? (
        <div style={{ marginTop: 14, display: 'flex', alignItems: 'center', gap: 12, padding: '12px 14px', border: '1px solid var(--border)', borderRadius: 9, background: 'var(--s2)' }}>
          <span style={{ fontSize: 13, color: 'var(--muted)', flex: 1 }}>
            Couldn't read this deployment's settings, so it isn't known whether feedback can be enabled here.
          </span>
          <button onClick={() => void metaQ.refetch()} style={ghostButton}>
            Retry
          </button>
        </div>
      ) : storageUnknown || cfgQ.isLoading ? (
        <div className="om-skel" style={{ height: 96, width: '100%', marginTop: 14, borderRadius: 9 }} />
      ) : !storageReady ? (
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 13, marginTop: 14, padding: 14, border: '1px dashed var(--border-strong)', borderRadius: 9, background: 'var(--s2)' }}>
          <span aria-hidden style={{ display: 'grid', placeItems: 'center', height: 36, width: 36, borderRadius: 9, background: 'var(--s3)', color: 'var(--muted)', flex: 'none' }}>⏻</span>
          <div>
            <div style={{ fontSize: 13.5, fontWeight: 700, marginBottom: 3 }}>Feedback can't be turned on here</div>
            <p style={{ margin: '0 0 10px', fontSize: 12.5, color: 'var(--muted)', maxWidth: '66ch' }}>
              This deployment has no object storage configured, so there is nowhere to put attachments — the API answers{' '}
              <code style={{ fontFamily: 'var(--mono)' }}>503</code> to any attempt to enable it. Set{' '}
              <code style={{ fontFamily: 'var(--mono)' }}>STATUS_FEEDBACK_*</code> and redeploy; the switch becomes
              available on its own.
            </p>
            <Toggle checked={false} disabled label="Unavailable" onChange={() => {}} />
          </div>
        </div>
      ) : /* ⚠ Not `cfgQ.isError || !cfg`: a save invalidates this key, so a failed
             background refetch would replace the panel — toggles, key row and
             snippet — with an error card immediately after a save that worked. */
      !cfg ? (
        <div style={{ marginTop: 14, display: 'flex', alignItems: 'center', gap: 12, padding: '12px 14px', border: '1px solid var(--border)', borderRadius: 9, background: 'var(--s2)' }}>
          <span style={{ fontSize: 13, color: 'var(--muted)', flex: 1 }}>Couldn't load the feedback configuration.</span>
          <button onClick={() => void cfgQ.refetch()} style={ghostButton}>
            Retry
          </button>
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: 18, marginTop: 14 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 14, paddingTop: 2 }}>
              <div>
                <div style={{ fontSize: 13, fontWeight: 600 }}>Feedback enabled</div>
                <div style={{ fontSize: 11.5, color: 'var(--muted)' }}>Off stops the widget rendering on the next load</div>
              </div>
              <Toggle
                checked={cfg.enabled}
                disabled={!isAdmin || update.isPending}
                label="Feedback enabled"
                onChange={(v) => update.mutate({ enabled: v })}
              />
            </div>

            <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 14, paddingTop: 12, borderTop: '1px solid var(--border)' }}>
              <div>
                <div style={{ fontSize: 13, fontWeight: 600 }}>Send console output</div>
                <div style={{ fontSize: 11.5, color: 'var(--muted)', maxWidth: '44ch' }}>
                  Attaches the last 50 console lines and the last JS error. Off by default — a console line can carry
                  private content out of the host app. The reporter sees the lines before sending.
                </div>
              </div>
              <Toggle
                checked={cfg.console_capture}
                disabled={!isAdmin || update.isPending}
                label="Send console output"
                onChange={(v) => update.mutate({ console_capture: v })}
              />
            </div>

            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap', paddingTop: 12, borderTop: '1px solid var(--border)' }}>
              <div>
                <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--subtle)' }}>Widget key</div>
                <div style={{ fontSize: 12.5, color: 'var(--muted)' }}>
                  {cfg.widget_key_set_at ? `Issued ${relativeTime(cfg.widget_key_set_at)} · never shown again` : 'Not issued yet — turn feedback on'}
                </div>
              </div>
              {isAdmin && cfg.widget_key_set_at && (
                <button onClick={() => setConfirmRotate(true)} style={{ ...ghostButton, height: 34 }}>
                  Rotate widget key
                </button>
              )}
            </div>
            <p style={{ margin: 0, fontSize: 11.5, color: 'var(--subtle)' }}>
              Separate from the ingest key on purpose: rotating this one never interrupts crash reporting.
            </p>
          </div>

          <div>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--subtle)', marginBottom: 5 }}>Embed snippet</div>
            <CopyableCode code={widgetEmbedSnippet(siteId)} />
            <p style={{ margin: '10px 0 0', fontSize: 11.5, color: 'var(--subtle)' }}>
              The key is written into the host page, like an ingest key.{' '}
              <code style={{ fontFamily: 'var(--mono)' }}>data-launcher="none"</code> hides the floating button and
              leaves <code style={{ fontFamily: 'var(--mono)' }}>StatusFeedback.open()</code> for a host menu item. The
              rest of the attributes are in <code style={{ fontFamily: 'var(--mono)' }}>docs/widget.md</code>.
            </p>
          </div>
        </div>
      )}

      {confirmRotate && (
        <ConfirmDialog
          title="Rotate the widget key?"
          body="The current key stops working immediately, and every page still embedding it stops showing the reporting button until the snippet is updated. Crash reporting is unaffected — that is a different key."
          confirmLabel="Rotate key"
          onCancel={() => setConfirmRotate(false)}
          onConfirm={() => {
            setConfirmRotate(false)
            rotate.mutate()
          }}
        />
      )}
      {issuedKey && (
        <KeyModal
          title="Widget key"
          subtitle={`Feedback key for ${siteId} — paste the whole snippet`}
          keyValue={issuedKey}
          snippet={widgetEmbedSnippet(siteId, issuedKey)}
          snippetLabel="Ready to paste into the host page"
          onClose={() => setIssuedKey(null)}
        />
      )}
    </div>
  )
}

function Toggle({
  checked,
  disabled,
  label,
  onChange,
}: {
  checked: boolean
  disabled?: boolean
  label: string
  onChange: (v: boolean) => void
}) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      style={{
        position: 'relative',
        flex: 'none',
        width: 40,
        height: 23,
        borderRadius: 999,
        padding: 0,
        cursor: disabled ? 'default' : 'pointer',
        opacity: disabled ? 0.5 : 1,
        background: checked ? 'var(--accent)' : 'var(--s4)',
        border: `1px solid ${checked ? 'var(--accent)' : 'var(--border-strong)'}`,
      }}
    >
      <span
        aria-hidden
        style={{
          position: 'absolute',
          top: 2,
          left: checked ? 19 : 2,
          height: 17,
          width: 17,
          borderRadius: '50%',
          background: checked ? 'var(--accent-fg)' : 'var(--muted)',
          transition: 'left .14s ease',
        }}
      />
    </button>
  )
}
