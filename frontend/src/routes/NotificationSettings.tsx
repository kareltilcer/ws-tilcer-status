import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import * as api from '@/api/endpoints'
import { ApiError } from '@/api/client'
import { qk } from '@/api/keys'
import { useAuth } from '@/app/auth'
import { StateBlock, Toggle, cardStyle, fieldLabel, ghostButton, inputStyle, primaryButton } from '@/components/ui'
import { relativeTime } from '@/lib/format'
import type { DeliveryState, NotificationEvents, NotificationSettings as Settings, NotificationTestResult } from '@/api/types'

/**
 * NotificationSettings is where email notifications are configured: who gets
 * them, about what, which sites stay quiet, and what became of the last few.
 *
 * ⚠ It is gated on the deployment, not on the settings row. With no mail
 * provider every save that turns them on is a 503, so the page says so instead of
 * offering controls whose only answer is an error — the FeedbackPanel rule.
 */
export function NotificationSettings() {
  const metaQ = useQuery({ queryKey: qk.meta(), queryFn: () => api.getMeta(), staleTime: 5 * 60_000 })
  // ⚠ Three states, not two: `undefined` is both "still loading" and "failed".
  const available = metaQ.data?.notifications_enabled === true
  const unknown = metaQ.data === undefined

  const settingsQ = useQuery({
    queryKey: qk.notificationSettings(),
    queryFn: () => api.getNotificationSettings(),
    enabled: available,
  })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div>
        <h1 style={{ margin: '0 0 4px', fontSize: 24, fontWeight: 800, letterSpacing: '-.01em' }}>Notifications</h1>
        <p style={{ margin: 0, fontSize: 13.5, color: 'var(--muted)', maxWidth: '70ch' }}>
          Email when something happens that the board would otherwise only show to whoever is looking: a new crash, a
          resolved crash that came back, a feedback report, a site going down and coming back up.
        </p>
      </div>

      {unknown && metaQ.isError ? (
        <StateBlock icon="⚠" danger title="Couldn't read this deployment's settings" body="It isn't known whether this deployment can send email.">
          <button onClick={() => void metaQ.refetch()} style={ghostButton}>
            Retry
          </button>
        </StateBlock>
      ) : unknown || (available && settingsQ.isLoading) ? (
        <div className="om-skel" style={{ height: 320, width: '100%', borderRadius: 'var(--radius)' }} />
      ) : !available ? (
        <div style={{ ...cardStyle, display: 'flex', alignItems: 'flex-start', gap: 13, borderStyle: 'dashed' }}>
          <span aria-hidden style={{ display: 'grid', placeItems: 'center', height: 36, width: 36, borderRadius: 9, background: 'var(--s3)', color: 'var(--muted)', flex: 'none' }}>
            ✉
          </span>
          <div>
            <div style={{ fontSize: 13.5, fontWeight: 700, marginBottom: 3 }}>Notifications can't be turned on here</div>
            <p style={{ margin: 0, fontSize: 12.5, color: 'var(--muted)', maxWidth: '66ch' }}>
              This deployment has no mail provider, so the API answers <code style={{ fontFamily: 'var(--mono)' }}>503</code> to
              any attempt to turn them on. Set <code style={{ fontFamily: 'var(--mono)' }}>STATUS_RESEND_API_KEY</code> (and a
              verified <code style={{ fontFamily: 'var(--mono)' }}>STATUS_MAIL_FROM</code>) and redeploy; this page becomes
              available on its own.
            </p>
          </div>
        </div>
      ) : !settingsQ.data ? (
        <StateBlock icon="⚠" danger title="Couldn't load the notification settings" body="Nothing was changed.">
          <button onClick={() => void settingsQ.refetch()} style={ghostButton}>
            Retry
          </button>
        </StateBlock>
      ) : (
        <Loaded settings={settingsQ.data} />
      )}
    </div>
  )
}

function Loaded({ settings }: { settings: Settings }) {
  const [dirty, setDirty] = useState(false)
  return (
    <>
      {settings.provider === 'log' && (
        <div style={{ padding: '10px 14px', border: '1px solid var(--border)', borderRadius: 9, background: 'var(--s2)', fontSize: 12.5, color: 'var(--muted)' }}>
          Development deployment: emails are written to the backend log, not delivered.
        </div>
      )}
      <DeliveryCard settings={settings} onDirtyChange={setDirty} />
      <TestCard settings={settings} dirty={dirty} />
      <SitesCard settings={settings} />
      <DeliveriesCard />
    </>
  )
}

// --- delivery ---------------------------------------------------------------

const EVENT_ROWS: { key: keyof NotificationEvents; title: string; hint: string }[] = [
  {
    key: 'crash',
    title: 'New crashes, and crashes that come back',
    hint: 'A crash group’s first error or fatal event from production (environment prod, production or unset), and a resolved group reopened by a new event. Warnings and dev builds never email.',
  },
  { key: 'feedback', title: 'Feedback reports', hint: 'Every report sent from the widget.' },
  { key: 'downtime', title: 'Sites going down and back up', hint: 'When a site turns red, and again at its next passing check.' },
]

/** parseRecipients splits what was typed on commas, semicolons and whitespace.
 *  The server is the validator; this only decides what the list IS. */
function parseRecipients(text: string): string[] {
  return text
    .split(/[\s,;]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function formatWindow(seconds: number): string {
  if (seconds <= 0) return 'no time'
  if (seconds < 60) return `${seconds} s`
  const mins = Math.round(seconds / 60)
  return mins === 1 ? '1 minute' : `${mins} minutes`
}

function DeliveryCard({ settings, onDirtyChange }: { settings: Settings; onDirtyChange: (dirty: boolean) => void }) {
  const qc = useQueryClient()
  const { isAdmin } = useAuth()
  const saved = useMemo(
    () => ({ enabled: settings.enabled, recipients: (settings.recipients ?? []).join(', '), events: settings.events }),
    [settings],
  )
  const [enabled, setEnabled] = useState(saved.enabled)
  const [recipients, setRecipients] = useState(saved.recipients)
  const [events, setEvents] = useState<NotificationEvents>(saved.events)

  // Re-sync when the server's copy changes (a save, another tab). Query data is
  // structurally shared, so an unchanged refetch keeps the same object and does
  // not wipe what is being typed.
  useEffect(() => {
    setEnabled(saved.enabled)
    setRecipients(saved.recipients)
    setEvents(saved.events)
  }, [saved])

  const dirty =
    enabled !== saved.enabled ||
    parseRecipients(recipients).join(',') !== parseRecipients(saved.recipients).join(',') ||
    (Object.keys(events) as (keyof NotificationEvents)[]).some((k) => events[k] !== saved.events[k])
  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange])

  const save = useMutation({
    mutationFn: () => api.updateNotificationSettings({ enabled, recipients: parseRecipients(recipients), events }),
    onSuccess: (next) => {
      qc.setQueryData(qk.notificationSettings(), next)
      // Switching off cancels what was waiting; the list should show it.
      void qc.invalidateQueries({ queryKey: qk.notificationDeliveries() })
      toast.success(next.enabled ? 'Notifications saved' : 'Notifications are off')
    },
    onError: (e) => toast.error(e instanceof ApiError && e.detail ? e.detail : 'Could not save the notification settings'),
  })

  function onSave() {
    const list = parseRecipients(recipients)
    if (enabled && list.length === 0) {
      toast.error('Add at least one recipient before turning notifications on')
      return
    }
    if (list.length > 5) {
      toast.error('At most 5 recipients')
      return
    }
    save.mutate()
  }

  const locked = !isAdmin || save.isPending
  return (
    <div style={cardStyle}>
      <h2 style={{ margin: '0 0 3px', fontSize: 15, fontWeight: 700 }}>Delivery</h2>
      <p style={{ margin: '0 0 14px', fontSize: 12.5, color: 'var(--muted)', maxWidth: '70ch' }}>
        Emails are grouped: the first notification waits {formatWindow(settings.digest_window_seconds)} for company, then
        everything waiting goes out in one email. At most {settings.max_per_hour} emails an hour — beyond that they wait
        and go out together; nothing is dropped. Sent from <span style={{ fontFamily: 'var(--mono)' }}>{settings.from}</span>.
      </p>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <SettingRow title="Email notifications" hint="Off: nothing is sent, and anything still waiting is cancelled.">
          <Toggle checked={enabled} disabled={locked} label="Email notifications" onChange={setEnabled} />
        </SettingRow>

        <label style={{ display: 'block', paddingTop: 12, borderTop: '1px solid var(--border)' }}>
          {fieldLabel('Recipients')}
          {settings.recipients === null ? (
            <div style={{ fontSize: 12.5, color: 'var(--muted)' }}>Only an admin can see who gets these emails.</div>
          ) : (
            <>
              <textarea
                value={recipients}
                onChange={(e) => setRecipients(e.target.value)}
                disabled={locked}
                rows={2}
                spellCheck={false}
                placeholder="you@example.com"
                style={{ ...inputStyle, height: 'auto', minHeight: 60, padding: '9px 12px', resize: 'vertical', fontFamily: 'var(--mono)', fontSize: 13 }}
              />
              <span style={{ display: 'block', marginTop: 5, fontSize: 11.5, color: 'var(--muted)' }}>
                Up to 5 plain addresses, separated by commas or new lines. They share one To: line, so everyone on it sees
                the others.
              </span>
            </>
          )}
        </label>

        {EVENT_ROWS.map((row) => (
          <SettingRow key={row.key} title={row.title} hint={row.hint}>
            <Toggle
              checked={events[row.key]}
              disabled={locked}
              label={row.title}
              onChange={(v) => setEvents((prev) => ({ ...prev, [row.key]: v }))}
            />
          </SettingRow>
        ))}

        {isAdmin && (
          <div style={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 12, marginTop: 4 }}>
            {settings.updated_at && <span style={{ fontSize: 11.5, color: 'var(--subtle)' }}>Saved {relativeTime(settings.updated_at)}</span>}
            <button onClick={onSave} disabled={!dirty || save.isPending} style={{ ...primaryButton, height: 36, opacity: !dirty ? 0.55 : 1 }}>
              Save changes
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

function SettingRow({ title, hint, children }: { title: string; hint: string; children: ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 14, paddingTop: 12, borderTop: '1px solid var(--border)' }}>
      <div>
        <div style={{ fontSize: 13, fontWeight: 600 }}>{title}</div>
        <div style={{ fontSize: 11.5, color: 'var(--muted)', maxWidth: '62ch' }}>{hint}</div>
      </div>
      {children}
    </div>
  )
}

// --- test -------------------------------------------------------------------

function TestCard({ settings, dirty }: { settings: Settings; dirty: boolean }) {
  const { isAdmin } = useAuth()
  const [result, setResult] = useState<{ ok: true; res: NotificationTestResult } | { ok: false; detail: string } | null>(null)
  const send = useMutation({
    mutationFn: () => api.sendTestNotification(),
    onMutate: () => setResult(null),
    onSuccess: (res) => setResult({ ok: true, res }),
    onError: (e) =>
      setResult({
        ok: false,
        detail:
          e instanceof ApiError && e.status === 429
            ? 'Wait a few seconds before sending another test email.'
            : e instanceof ApiError && e.detail
              ? e.detail
              : 'The test email could not be sent.',
      }),
  })
  if (!isAdmin) return null
  const noRecipients = (settings.recipients ?? []).length === 0
  const blocked = dirty ? 'Save your changes first — the test goes to the saved recipients.' : noRecipients ? 'Save a recipient first.' : null

  return (
    <div style={cardStyle}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 14, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ margin: '0 0 3px', fontSize: 15, fontWeight: 700 }}>Test email</h2>
          <p style={{ margin: 0, fontSize: 12.5, color: 'var(--muted)', maxWidth: '62ch' }}>
            Sends one email now to the saved recipients — even while notifications are off — to prove the sender domain and
            the addresses work before anything real depends on them.
          </p>
        </div>
        <button onClick={() => send.mutate()} disabled={!!blocked || send.isPending} style={{ ...ghostButton, opacity: blocked ? 0.55 : 1 }}>
          {send.isPending ? 'Sending…' : 'Send test email'}
        </button>
      </div>
      {blocked && <p style={{ margin: '10px 0 0', fontSize: 12, color: 'var(--subtle)' }}>{blocked}</p>}
      {result && (
        <div
          role="status"
          style={{
            marginTop: 12,
            padding: '10px 12px',
            borderRadius: 9,
            fontSize: 12.5,
            border: `1px solid ${result.ok ? 'color-mix(in oklab, var(--good) 40%, transparent)' : 'color-mix(in oklab, var(--danger) 45%, transparent)'}`,
            background: result.ok ? 'color-mix(in oklab, var(--good) 10%, transparent)' : 'color-mix(in oklab, var(--danger) 12%, transparent)',
            color: result.ok ? 'var(--good-text)' : 'var(--danger-text)',
          }}
        >
          {result.ok
            ? settings.provider === 'log'
              ? `Logged for ${result.res.recipients.join(', ')} — development, nothing was delivered.`
              : `Accepted for ${result.res.recipients.join(', ')}${result.res.provider_message_id ? ` (id ${result.res.provider_message_id})` : ''}. Check the inbox — and the spam folder.`
            : result.detail}
        </div>
      )}
    </div>
  )
}

// --- sites ------------------------------------------------------------------

function SitesCard({ settings }: { settings: Settings }) {
  const qc = useQueryClient()
  const { isAdmin } = useAuth()
  // Shares the board's query — one request, many readers.
  const sitesQ = useQuery({ queryKey: qk.sites(), queryFn: () => api.listSites(), staleTime: 30_000 })
  const muted = new Set(settings.muted_sites)
  const toggle = useMutation({
    mutationFn: ({ id, notify }: { id: string; notify: boolean }) => (notify ? api.unmuteSite(id) : api.muteSite(id)),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.notificationSettings() }),
    onError: (e) => toast.error(e instanceof ApiError && e.detail ? e.detail : 'Could not change the site'),
  })
  const sites = sitesQ.data ?? []

  return (
    <div style={cardStyle}>
      <h2 style={{ margin: '0 0 3px', fontSize: 15, fontWeight: 700 }}>Sites</h2>
      <p style={{ margin: '0 0 6px', fontSize: 12.5, color: 'var(--muted)', maxWidth: '62ch' }}>
        A muted site sends nothing, and anything already waiting for it is dropped. The board is unaffected.
      </p>
      {sitesQ.isLoading ? (
        <div className="om-skel" style={{ height: 80, width: '100%', borderRadius: 9, marginTop: 8 }} />
      ) : sitesQ.isError ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 8 }}>
          <span style={{ fontSize: 13, color: 'var(--muted)', flex: 1 }}>Couldn't load the sites.</span>
          <button onClick={() => void sitesQ.refetch()} style={ghostButton}>
            Retry
          </button>
        </div>
      ) : sites.length === 0 ? (
        <p style={{ margin: '8px 0 0', fontSize: 13, color: 'var(--muted)' }}>No sites yet.</p>
      ) : (
        sites.map((s) => {
          const notify = !muted.has(s.id)
          return (
            <div key={s.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 14, padding: '10px 0', borderTop: '1px solid var(--border)' }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.name}</div>
                <div style={{ fontSize: 11.5, color: 'var(--muted)', fontFamily: 'var(--mono)' }}>
                  {s.id}
                  {!notify && ' · muted'}
                </div>
              </div>
              <Toggle
                checked={notify}
                disabled={!isAdmin || toggle.isPending}
                label={`Email about ${s.name}`}
                onChange={(v) => toggle.mutate({ id: s.id, notify: v })}
              />
            </div>
          )
        })
      )}
    </div>
  )
}

// --- deliveries ---------------------------------------------------------------

const STATE_STYLE: Record<DeliveryState, { label: string; style: CSSProperties }> = {
  sent: { label: 'Sent', style: { color: 'var(--good-text)', background: 'color-mix(in oklab, var(--good) 16%, transparent)' } },
  pending: { label: 'Waiting', style: { color: 'var(--warn-text)', background: 'color-mix(in oklab, var(--warn) 14%, transparent)' } },
  failed: { label: 'Failed', style: { color: 'var(--danger-text)', background: 'var(--danger-soft)' } },
}

function DeliveriesCard() {
  const q = useQuery({
    queryKey: qk.notificationDeliveries(),
    queryFn: () => api.listDeliveries(20),
    refetchInterval: 30_000,
  })
  const items = q.data?.items ?? []
  return (
    <div style={cardStyle}>
      <h2 style={{ margin: '0 0 3px', fontSize: 15, fontWeight: 700 }}>Recent emails</h2>
      <p style={{ margin: '0 0 6px', fontSize: 12.5, color: 'var(--muted)' }}>
        A failed send is retried for up to 23 hours; a refusal that no retry can fix, or switching notifications off,
        ends it.
      </p>
      {q.isLoading ? (
        <div className="om-skel" style={{ height: 80, width: '100%', borderRadius: 9, marginTop: 8 }} />
      ) : q.isError && !q.data ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 8 }}>
          <span style={{ fontSize: 13, color: 'var(--muted)', flex: 1 }}>Couldn't load the recent emails.</span>
          <button onClick={() => void q.refetch()} style={ghostButton}>
            Retry
          </button>
        </div>
      ) : items.length === 0 ? (
        <p style={{ margin: '8px 0 0', fontSize: 13, color: 'var(--muted)' }}>Nothing sent yet.</p>
      ) : (
        items.map((d) => {
          const st = STATE_STYLE[d.state]
          return (
            <div key={d.id} style={{ padding: '10px 0', borderTop: '1px solid var(--border)' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
                <span style={{ flex: 'none', padding: '2px 8px', borderRadius: 999, fontSize: 11, fontWeight: 700, ...st.style }}>{st.label}</span>
                <span style={{ fontSize: 13, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>{d.subject}</span>
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 14px', marginTop: 4, fontSize: 11.5, color: 'var(--muted)' }}>
                <span>{relativeTime(d.created_at)}</span>
                <span>
                  {d.event_count} {d.event_count === 1 ? 'notification' : 'notifications'}
                </span>
                {d.attempts > 1 && <span>{d.attempts} attempts</span>}
                {d.state === 'pending' && d.next_attempt_at && d.attempts > 0 && <span>retrying {relativeTimeAhead(d.next_attempt_at)}</span>}
                {d.recipients && <span style={{ fontFamily: 'var(--mono)' }}>{d.recipients.join(', ')}</span>}
              </div>
              {d.last_error && d.state !== 'sent' && (
                <div style={{ marginTop: 4, fontSize: 11.5, color: d.state === 'failed' ? 'var(--danger-text)' : 'var(--subtle)', wordBreak: 'break-word' }}>{d.last_error}</div>
              )}
            </div>
          )
        })
      )}
    </div>
  )
}

/** relativeTimeAhead renders a future timestamp as "in 3m". */
function relativeTimeAhead(iso: string): string {
  const secs = Math.round((new Date(iso).getTime() - Date.now()) / 1000)
  if (Number.isNaN(secs) || secs <= 5) return 'shortly'
  if (secs < 60) return `in ${secs}s`
  const mins = Math.round(secs / 60)
  if (mins < 60) return `in ${mins}m`
  return `in ${Math.round(mins / 60)}h`
}
