import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import * as api from '@/api/endpoints'
import { ApiError } from '@/api/client'
import { qk } from '@/api/keys'
import { paths } from '@/app/routes'
import { COLOR, StatusPill, cardStyle, inputStyle, fieldLabel, primaryButton, ghostButton } from '@/components/ui'
import { LevelBadge } from '@/components/ui'
import { UptimeStrip } from '@/components/UptimeStrip'
import { CodeSnippet } from '@/components/CodeSnippet'
import { KeyModal } from '@/components/KeyModal'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { useMediaQuery } from '@/lib/useMediaQuery'
import { relativeTime, uptimeWindow } from '@/lib/format'
import type { CrashGroup, GroupStatus, SiteSummary } from '@/api/types'

const groupStatusColor: Record<GroupStatus, { fg: string; bg: string }> = {
  open: { fg: 'var(--warn-text)', bg: 'color-mix(in oklab, var(--warn) 14%, transparent)' },
  resolved: { fg: 'var(--good-text)', bg: 'color-mix(in oklab, var(--good) 16%, transparent)' },
  ignored: { fg: 'var(--subtle)', bg: 'var(--s3)' },
}

export function SiteDetail() {
  const { id } = useParams()
  const nav = useNavigate()
  const qc = useQueryClient()
  const isMobile = useMediaQuery('(max-width: 720px)')
  const [rotateKeyValue, setRotateKeyValue] = useState<string | null>(null)
  const [confirm, setConfirm] = useState<null | 'delete' | 'rotate'>(null)

  const siteQ = useQuery({ queryKey: qk.site(id!), queryFn: () => api.getSite(id!), enabled: !!id })
  const site = siteQ.data
  const metaQ = useQuery({ queryKey: qk.meta(), queryFn: () => api.getMeta(), staleTime: 5 * 60_000 })
  // Drive the strip from the configured window so this page and the board describe
  // the same uptime span (snapped to the nearest enum the /uptime endpoint accepts).
  const uptimeWin = uptimeWindow(metaQ.data?.uptime_window_days ?? 90)
  const uptimeQ = useQuery({
    queryKey: qk.uptime(id!, uptimeWin.window),
    queryFn: () => api.getUptime(id!, uptimeWin.window, uptimeWin.buckets),
    enabled: !!id && !!site?.monitor_enabled,
  })
  const crashesQ = useQuery({ queryKey: qk.crashes(id!, {}), queryFn: () => api.listCrashes(id!, {}), enabled: !!id })

  const del = useMutation({
    mutationFn: () => api.deleteSite(id!),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.sites() })
      toast.success('Site deleted')
      nav(paths.board)
    },
  })
  const rotate = useMutation({
    mutationFn: () => api.rotateKey(id!),
    onSuccess: (r) => setRotateKeyValue(r.ingest_key),
  })

  if (siteQ.isLoading) {
    return (
      <div>
        <div className="om-skel" style={{ height: 26, width: 220, marginBottom: 20 }} />
        <div style={{ ...cardStyle, marginBottom: 16 }}><div className="om-skel" style={{ height: 30, width: '100%' }} /></div>
      </div>
    )
  }
  if (siteQ.isError || !site) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', minHeight: 340, textAlign: 'center' }}>
        <div style={{ maxWidth: 360 }}>
          <div style={{ margin: '0 auto 16px', display: 'grid', placeItems: 'center', height: 56, width: 56, borderRadius: 14, background: 'var(--danger-soft)', color: 'var(--danger-text)', fontSize: 24 }}>⚠</div>
          <div style={{ fontSize: 18, fontWeight: 700, marginBottom: 6 }}>Site not found</div>
          <p style={{ margin: '0 0 18px', fontSize: 13.5, color: 'var(--muted)' }}>This site may have been deleted. Head back to the board.</p>
          <button onClick={() => nav(paths.board)} style={ghostButton}>Back to board</button>
        </div>
      </div>
    )
  }

  return (
    <div>
      <button onClick={() => nav(paths.board)} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, background: 'none', border: 'none', color: 'var(--muted)', fontSize: 13, fontWeight: 600, cursor: 'pointer', padding: 0, marginBottom: 14, fontFamily: 'inherit' }}>‹ Board</button>

      <Header site={site} onRotate={() => setConfirm('rotate')} onDelete={() => setConfirm('delete')} />

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* uptime */}
        <div style={cardStyle}>
          <h2 style={{ margin: '0 0 14px', fontSize: 15, fontWeight: 700 }}>Uptime <span style={{ fontWeight: 500, color: 'var(--subtle)', fontSize: 13 }}>· {uptimeWin.label}</span></h2>
          {!site.monitor_enabled ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 14, padding: 16, border: '1px dashed var(--border-strong)', borderRadius: 9, background: 'var(--s2)' }}>
              <span style={{ display: 'grid', placeItems: 'center', height: 40, width: 40, borderRadius: 10, background: 'var(--s3)', color: 'var(--muted)', flex: 'none' }}>⏻</span>
              <div>
                <div style={{ fontSize: 13.5, fontWeight: 700, marginBottom: 2 }}>Monitoring is off</div>
                <p style={{ margin: 0, fontSize: 12.5, color: 'var(--muted)' }}>This is a crash-only site — no uptime checks are scheduled, so there's no availability to report. Its status reflects crashes only and it will never go red.</p>
              </div>
            </div>
          ) : uptimeQ.data ? (
            <UptimeStrip summary={uptimeQ.data} />
          ) : (
            <div style={{ display: 'grid', placeItems: 'center', minHeight: 60, color: 'var(--muted)', fontSize: 13 }}>No checks yet — the first poll runs within a few minutes.</div>
          )}
        </div>

        {/* crashes */}
        <div style={cardStyle}>
          <h2 style={{ margin: '0 0 14px', fontSize: 15, fontWeight: 700 }}>Crashes</h2>
          {crashesQ.isError ? (
            <div style={{ display: 'grid', placeItems: 'center', minHeight: 120, textAlign: 'center' }}>
              <div style={{ maxWidth: 340 }}>
                <div style={{ margin: '0 auto 10px', display: 'grid', placeItems: 'center', height: 44, width: 44, borderRadius: 12, background: 'var(--danger-soft)', color: 'var(--danger-text)', fontSize: 20 }}>⚠</div>
                <div style={{ fontSize: 14.5, fontWeight: 700, marginBottom: 4 }}>Couldn't load crashes</div>
                <p style={{ margin: '0 0 12px', fontSize: 13, color: 'var(--muted)' }}>The request failed — this does not mean the site is healthy. Retry to check again.</p>
                <button onClick={() => void crashesQ.refetch()} style={ghostButton}>Retry</button>
              </div>
            </div>
          ) : !crashesQ.data ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
              {[0, 1, 2].map((i) => (
                <div key={i} className="om-skel" style={{ height: 62, width: '100%', borderRadius: 'var(--radius)' }} />
              ))}
            </div>
          ) : crashesQ.data.items.length > 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
              {crashesQ.data.items.map((g) => (
                <GroupRow key={g.id} g={g} onClick={() => nav(paths.group(g.id))} />
              ))}
            </div>
          ) : (
            <div style={{ display: 'grid', placeItems: 'center', minHeight: 120, textAlign: 'center' }}>
              <div style={{ maxWidth: 320 }}>
                <div style={{ fontSize: 30, marginBottom: 8 }}>🎉</div>
                <div style={{ fontSize: 15, fontWeight: 700, marginBottom: 4 }}>No crashes</div>
                <p style={{ margin: 0, fontSize: 13, color: 'var(--muted)' }}>Nothing's been reported here yet. Wire up the ingest snippet and this fills in the moment something throws.</p>
              </div>
            </div>
          )}
        </div>

        {/* config + integration */}
        <div style={{ display: 'grid', gridTemplateColumns: isMobile ? '1fr' : 'repeat(auto-fit, minmax(320px, 1fr))', gap: 16 }}>
          <ConfigEditor site={site} />
          <div style={cardStyle}>
            <h2 style={{ margin: '0 0 14px', fontSize: 15, fontWeight: 700 }}>Integration</h2>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--subtle)', marginBottom: 5 }}>Ingest endpoint</div>
            <code style={{ display: 'block', fontFamily: 'var(--mono)', fontSize: 12, background: 'var(--s2)', border: '1px solid var(--border)', borderRadius: 8, padding: '9px 11px', marginBottom: 16, overflowX: 'auto', whiteSpace: 'nowrap' }}>POST /api/ingest/{site.id}</code>
            <CodeSnippet siteId={site.id} />
          </div>
        </div>
      </div>

      {confirm === 'delete' && (
        <ConfirmDialog
          title="Delete this site?"
          body={`Deleting "${site.name}" permanently removes it and cascades all of its checks, rollups, crash groups, and events. This cannot be undone.`}
          confirmLabel="Delete site"
          danger
          onCancel={() => setConfirm(null)}
          onConfirm={() => { setConfirm(null); del.mutate() }}
        />
      )}
      {confirm === 'rotate' && (
        <ConfirmDialog
          title="Rotate the ingest key?"
          body="The current key stops working immediately. Any client still using it will get 401 until you paste in the new key."
          confirmLabel="Rotate key"
          onCancel={() => setConfirm(null)}
          onConfirm={() => { setConfirm(null); rotate.mutate() }}
        />
      )}
      {rotateKeyValue && (
        <KeyModal
          title="New ingest key"
          subtitle={`Rotated key for ${site.name} (${site.id})`}
          siteId={site.id}
          ingestKey={rotateKeyValue}
          onClose={() => setRotateKeyValue(null)}
        />
      )}
    </div>
  )
}

function Header({ site, onRotate, onDelete }: { site: SiteSummary; onRotate: () => void; onDelete: () => void }) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap', marginBottom: 22 }}>
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', marginBottom: 6 }}>
          <h1 style={{ margin: 0, fontSize: 24, fontWeight: 800, letterSpacing: '-.01em' }}>{site.name}</h1>
          <StatusPill color={site.color} />
          {!site.monitor_enabled && (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '4px 10px', borderRadius: 999, fontSize: 12, fontWeight: 600, border: '1px solid var(--border)', background: 'var(--s2)', color: 'var(--muted)' }}>⏻ Monitoring off</span>
          )}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 13, color: 'var(--muted)' }}>
          <span style={{ fontFamily: 'var(--mono)', color: 'var(--subtle)' }}>{site.id}</span>
          {site.monitor_url && (<><span style={{ opacity: 0.4 }}>·</span><span style={{ fontFamily: 'var(--mono)' }}>{site.monitor_url}</span></>)}
        </div>
      </div>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button onClick={onRotate} style={ghostButton}>Rotate key</button>
        <button onClick={onDelete} style={{ ...ghostButton, background: 'transparent', color: 'var(--danger-text)', border: '1px solid color-mix(in oklab, var(--danger) 45%, transparent)' }}>Delete</button>
      </div>
    </div>
  )
}

function GroupRow({ g, onClick }: { g: CrashGroup; onClick: () => void }) {
  const sc = groupStatusColor[g.status]
  return (
    <button onClick={onClick} style={{ ...cardStyle, padding: '12px 14px', cursor: 'pointer', textAlign: 'left', fontFamily: 'inherit', color: 'var(--text)', display: 'block', width: '100%' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <LevelBadge level={g.level} />
        <span style={{ flex: 1, minWidth: 0, fontSize: 13.5, fontWeight: 600, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{g.title}</span>
        <span style={{ flex: 'none', padding: '2px 9px', borderRadius: 999, fontSize: 11, fontWeight: 700, color: sc.fg, background: sc.bg }}>{g.status}</span>
        <span style={{ flex: 'none', color: 'var(--subtle)' }}>›</span>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap', marginTop: 8, paddingLeft: 2, fontSize: 12, color: 'var(--muted)' }}>
        <span style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600, color: 'var(--text)' }}>{g.count} events</span>
        <span>first {relativeTime(g.first_seen)}</span>
        <span>last {relativeTime(g.last_seen)}</span>
      </div>
    </button>
  )
}

function ConfigEditor({ site }: { site: SiteSummary }) {
  const qc = useQueryClient()
  const [name, setName] = useState(site.name)
  const [url, setUrl] = useState(site.monitor_url ?? '')
  const [enabled, setEnabled] = useState(site.monitor_enabled)
  const [expected, setExpected] = useState(String(site.expected_status))
  const [win, setWin] = useState(String(site.crash_window_hours))

  useEffect(() => {
    setName(site.name)
    setUrl(site.monitor_url ?? '')
    setEnabled(site.monitor_enabled)
    setExpected(String(site.expected_status))
    setWin(String(site.crash_window_hours))
  }, [site])

  const save = useMutation({
    mutationFn: () =>
      api.updateSite(site.id, {
        name,
        monitor_url: url.trim() ? url.trim() : null,
        monitor_enabled: enabled,
        expected_status: Number(expected) || 200,
        crash_window_hours: Number(win) || 24,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.site(site.id) })
      void qc.invalidateQueries({ queryKey: qk.sites() })
      toast.success('Configuration saved')
    },
    // Surface the server's field-level reason (e.g. "monitor_url is required to
    // enable monitoring") instead of a generic message the user can't act on.
    onError: (e) => toast.error(e instanceof ApiError && e.detail ? e.detail : 'Could not save configuration'),
  })

  // Enabling monitoring without a URL is rejected by the backend (422); catch it
  // client-side with a clear message rather than firing a request that silently fails.
  function onSave() {
    if (enabled && !url.trim()) {
      toast.error('A Monitor URL is required to enable monitoring')
      return
    }
    save.mutate()
  }

  return (
    <div style={cardStyle}>
      <h2 style={{ margin: '0 0 14px', fontSize: 15, fontWeight: 700 }}>Configuration</h2>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <label style={{ display: 'block' }}>{fieldLabel('Name')}<input value={name} onChange={(e) => setName(e.target.value)} style={inputStyle} /></label>
        <label style={{ display: 'block' }}>{fieldLabel('Monitor URL')}<input value={url} onChange={(e) => setUrl(e.target.value)} style={{ ...inputStyle, fontFamily: 'var(--mono)' }} /></label>
        <label style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, cursor: 'pointer' }}>
          <span>
            <span style={{ display: 'block', fontSize: 13, fontWeight: 600 }}>Monitoring enabled</span>
            <span style={{ fontSize: 11.5, color: 'var(--muted)' }}>Poll this URL periodically</span>
          </span>
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} style={{ width: 18, height: 18, accentColor: 'var(--accent)' }} />
        </label>
        <div style={{ display: 'flex', gap: 12 }}>
          <label style={{ flex: 1 }}>{fieldLabel('Expected status')}<input value={expected} onChange={(e) => setExpected(e.target.value)} style={{ ...inputStyle, fontFamily: 'var(--mono)' }} /></label>
          <label style={{ flex: 1 }}>{fieldLabel('Crash window (h)')}<input value={win} onChange={(e) => setWin(e.target.value)} style={{ ...inputStyle, fontFamily: 'var(--mono)' }} /></label>
        </div>
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 4 }}>
          <button onClick={onSave} disabled={save.isPending} style={{ ...primaryButton, height: 36 }}>Save changes</button>
        </div>
      </div>
    </div>
  )
}
