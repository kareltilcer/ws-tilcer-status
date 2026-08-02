import { useState, type CSSProperties, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import * as api from '@/api/endpoints'
import { qk } from '@/api/keys'
import { paths } from '@/app/routes'
import { COLOR, StatusPill, primaryButton } from '@/components/ui'
import { relativeTime, uptimeText } from '@/lib/format'
import type { Color, SiteSummary } from '@/api/types'

const COLORS: Color[] = ['green', 'orange', 'red', 'unknown']

export function Board() {
  const nav = useNavigate()
  const [filter, setFilter] = useState<Color | null>(null)
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: qk.sites(),
    queryFn: () => api.listSites(),
    refetchInterval: 30_000,
  })
  const { data: meta } = useQuery({ queryKey: qk.meta(), queryFn: () => api.getMeta(), staleTime: 5 * 60_000 })
  const windowDays = meta?.uptime_window_days

  const sites = data ?? []
  const shown = filter ? sites.filter((s) => s.color === filter) : sites
  const counts = (c: Color) => sites.filter((s) => s.color === c).length

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16, marginBottom: 6 }}>
        <div>
          <h1 style={{ margin: '0 0 4px', fontSize: 24, fontWeight: 800, letterSpacing: '-.01em' }}>Board</h1>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12.5, color: 'var(--muted)' }}>
            <span style={{ height: 7, width: 7, borderRadius: '50%', background: 'var(--good)', animation: 'om-live 2s ease-in-out infinite' }} />
            Auto-refreshes every 30s
          </div>
        </div>
        <button onClick={() => nav(paths.addSite)} style={primaryButton}>+ Add site</button>
      </div>

      {!isLoading && !isError && sites.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, margin: '18px 0' }}>
          <Chip label="All" count={sites.length} active={filter === null} onClick={() => setFilter(null)} />
          {COLORS.map((c) => (
            <Chip key={c} label={COLOR[c].name} count={counts(c)} dot={COLOR[c].fill} active={filter === c} onClick={() => setFilter(filter === c ? null : c)} />
          ))}
        </div>
      )}

      {isLoading && <SkeletonGrid />}
      {isError && (
        <StateBlock icon="⚠" title="Couldn't load the board" body="The API request failed. Retry, or check the backend is up.">
          <button onClick={() => void refetch()} style={{ ...primaryButton, background: 'var(--s2)', color: 'var(--text)', border: '1px solid var(--border)' }}>Retry</button>
        </StateBlock>
      )}
      {!isLoading && !isError && sites.length === 0 && (
        <StateBlock icon="◎" title="No sites yet" body="Add your first site to start watching it. You'll get an ingest key to wire up crash reporting in a couple of minutes.">
          <button onClick={() => nav(paths.addSite)} style={primaryButton}>+ Add your first site</button>
        </StateBlock>
      )}
      {!isLoading && !isError && sites.length > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(258px, 1fr))', gap: 14 }}>
          {shown.map((s) => (
            <SiteCard key={s.id} site={s} windowDays={windowDays} onClick={() => nav(paths.site(s.id))} />
          ))}
        </div>
      )}
    </div>
  )
}

function Chip({ label, count, dot, active, onClick }: { label: string; count: number; dot?: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 7,
        height: 30,
        padding: '0 11px',
        borderRadius: 999,
        border: `1px solid ${active ? 'var(--border-strong)' : 'var(--border)'}`,
        background: active ? 'var(--s3)' : 'var(--s1)',
        color: 'var(--text)',
        fontSize: 12.5,
        fontWeight: 600,
        cursor: 'pointer',
      }}
    >
      {dot && <span style={{ height: 8, width: 8, borderRadius: '50%', background: dot }} />}
      {label}
      <span style={{ opacity: 0.6, fontVariantNumeric: 'tabular-nums' }}>{count}</span>
    </button>
  )
}

const cardBtn: CSSProperties = {
  border: '1px solid var(--border)',
  background: 'var(--s1)',
  borderRadius: 'var(--radius)',
  padding: '15px 16px',
  boxShadow: 'var(--shadow)',
  cursor: 'pointer',
  textAlign: 'left',
  fontFamily: 'inherit',
  color: 'var(--text)',
  display: 'block',
  width: '100%',
}

function SiteCard({ site, windowDays, onClick }: { site: SiteSummary; windowDays?: number; onClick: () => void }) {
  const degrading = site.fail_streak > 0 && site.color !== 'red'
  const monitoringOff = !site.monitor_enabled
  return (
    <button onClick={onClick} style={cardBtn}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 10 }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{site.name}</div>
          <div style={{ fontFamily: 'var(--mono)', fontSize: 12, color: 'var(--subtle)' }}>{site.id}</div>
        </div>
        <StatusPill color={site.color} size="sm" />
      </div>
      {degrading && (
        <div style={{ marginTop: 9, fontSize: 11.5, color: 'var(--warn-text)', fontWeight: 600 }}>
          {site.fail_streak} failed check{site.fail_streak === 1 ? '' : 's'} · re-checking
        </div>
      )}
      <div style={{ marginTop: 14, display: 'flex', alignItems: 'center', gap: 14, fontSize: 12, color: 'var(--muted)' }}>
        <span title="Last checked">🕘 {relativeTime(site.last_checked_at)}</span>
        <span style={{ fontVariantNumeric: 'tabular-nums' }} title="Open crash groups">▦ {site.open_crash_groups}</span>
        <span style={{ fontVariantNumeric: 'tabular-nums' }} title="Recent crashes">🐞 {site.recent_crash_count}</span>
      </div>
      <div style={{ marginTop: 8, paddingTop: 9, borderTop: '1px solid var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, fontSize: 12, minHeight: 20 }}>
        {monitoringOff ? (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color: 'var(--muted)', fontWeight: 600 }}>⏻ Monitoring off</span>
        ) : (
          <>
            <span style={{ color: 'var(--subtle)' }}>Uptime{windowDays ? ` · ${windowDays}d` : ''}</span>
            <span style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 700, color: site.uptime_pct === null ? 'var(--muted)' : 'var(--good-text)' }}>
              {site.uptime_pct === null ? 'pending' : uptimeText(site.uptime_pct)}
            </span>
          </>
        )}
      </div>
    </button>
  )
}

function SkeletonGrid() {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(258px, 1fr))', gap: 14, marginTop: 18 }}>
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} style={{ border: '1px solid var(--border)', background: 'var(--s1)', borderRadius: 'var(--radius)', padding: '15px 16px' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10 }}>
            <div style={{ flex: 1 }}>
              <div className="om-skel" style={{ height: 14, width: '60%', marginBottom: 8 }} />
              <div className="om-skel" style={{ height: 11, width: '38%' }} />
            </div>
            <div className="om-skel" style={{ height: 20, width: 72, borderRadius: 999 }} />
          </div>
          <div className="om-skel" style={{ height: 12, width: '80%', marginTop: 18 }} />
        </div>
      ))}
    </div>
  )
}

function StateBlock({ icon, title, body, children }: { icon: string; title: string; body: string; children?: ReactNode }) {
  return (
    <div style={{ display: 'grid', placeItems: 'center', minHeight: 360, textAlign: 'center' }}>
      <div style={{ maxWidth: 380 }}>
        <div style={{ margin: '0 auto 16px', display: 'grid', placeItems: 'center', height: 56, width: 56, borderRadius: 14, background: 'var(--accent-soft)', color: 'var(--accent)', fontSize: 24 }}>{icon}</div>
        <div style={{ fontSize: 18, fontWeight: 700, marginBottom: 6 }}>{title}</div>
        <p style={{ margin: '0 0 18px', fontSize: 13.5, color: 'var(--muted)' }}>{body}</p>
        {children}
      </div>
    </div>
  )
}
