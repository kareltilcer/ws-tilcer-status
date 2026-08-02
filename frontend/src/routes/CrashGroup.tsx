import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import * as api from '@/api/endpoints'
import { qk } from '@/api/keys'
import { paths } from '@/app/routes'
import { LevelBadge, cardStyle, ghostButton } from '@/components/ui'
import { relativeTime } from '@/lib/format'
import type { CrashEvent, CrashGroup as CrashGroupType, GroupStatus } from '@/api/types'

export function CrashGroup() {
  const { groupId } = useParams()
  const gid = Number(groupId)
  const nav = useNavigate()
  const qc = useQueryClient()

  const q = useInfiniteQuery({
    queryKey: qk.crashGroup(gid),
    queryFn: ({ pageParam }) => api.getCrashGroup(gid, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: Number.isFinite(gid),
  })

  const triage = useMutation<CrashGroupType, unknown, GroupStatus>({
    mutationFn: (status) => api.triageGroup(gid, status),
    onSuccess: (updated) => {
      void qc.invalidateQueries({ queryKey: qk.crashGroup(gid) })
      void qc.invalidateQueries({ queryKey: qk.site(updated.site_id) })
      void qc.invalidateQueries({ queryKey: qk.sites() })
      toast.success(`Group ${updated.status}`)
    },
    onError: () => toast.error('Could not update the group'),
  })

  if (q.isLoading) {
    return (
      <div>
        <div className="om-skel" style={{ height: 28, width: '70%', marginBottom: 12 }} />
        <div className="om-skel" style={{ height: 14, width: '40%', marginBottom: 24 }} />
        <div className="om-skel" style={{ height: 120, width: '100%' }} />
      </div>
    )
  }
  if (q.isError || !q.data) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', minHeight: 340, textAlign: 'center' }}>
        <div style={{ maxWidth: 360 }}>
          <div style={{ margin: '0 auto 16px', display: 'grid', placeItems: 'center', height: 56, width: 56, borderRadius: 14, background: 'var(--danger-soft)', color: 'var(--danger-text)', fontSize: 24 }}>⚠</div>
          <div style={{ fontSize: 18, fontWeight: 700, marginBottom: 6 }}>Couldn't load this group</div>
          <p style={{ margin: '0 0 18px', fontSize: 13.5, color: 'var(--muted)' }}>It may have been purged by 90-day retention.</p>
          <button onClick={() => nav(paths.board)} style={ghostButton}>Back to board</button>
        </div>
      </div>
    )
  }

  const group = q.data.pages[0].group
  const events = q.data.pages.flatMap((p) => p.events)
  const latest = events[0]

  return (
    <div>
      <button onClick={() => nav(paths.site(group.site_id))} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, background: 'none', border: 'none', color: 'var(--muted)', fontSize: 13, fontWeight: 600, cursor: 'pointer', padding: 0, marginBottom: 14, fontFamily: 'inherit' }}>‹ {group.site_id}</button>

      {group.status === 'resolved' && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '11px 14px', borderRadius: 9, background: 'var(--good-soft)', border: '1px solid color-mix(in oklab, var(--good) 40%, transparent)', marginBottom: 18 }}>
          <span style={{ color: 'var(--good-text)' }}>✓</span>
          <div style={{ fontSize: 13, color: 'var(--text)' }}><b>Resolved.</b> This group no longer counts toward {group.site_id}'s orange signal. A new event will reopen it.</div>
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12, marginBottom: 6 }}>
        <span style={{ flex: 'none', marginTop: 3 }}><LevelBadge level={group.level} /></span>
        <h1 style={{ margin: 0, fontSize: 20, fontWeight: 700, lineHeight: 1.35, fontFamily: 'var(--mono)', letterSpacing: '-.01em' }}>{group.title}</h1>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap', margin: '12px 0 20px', fontSize: 12.5, color: 'var(--muted)' }}>
        <span style={{ padding: '3px 10px', borderRadius: 999, fontSize: 11.5, fontWeight: 700, color: 'var(--text)', background: 'var(--s3)' }}>{group.status}</span>
        <span style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 700, color: 'var(--text)' }}>{group.count} events</span>
        <span>first seen {relativeTime(group.first_seen)}</span>
        <span>last seen {relativeTime(group.last_seen)}</span>
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', gap: 8 }}>
          <button onClick={() => triage.mutate('resolved')} style={{ height: 34, padding: '0 13px', border: '1px solid var(--good)', background: 'transparent', color: 'var(--good-text)', borderRadius: 8, fontSize: 12.5, fontWeight: 700, cursor: 'pointer' }}>Resolve</button>
          <button onClick={() => triage.mutate('ignored')} style={{ height: 34, padding: '0 13px', border: '1px solid var(--border)', background: 'var(--s2)', color: 'var(--muted)', borderRadius: 8, fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}>Ignore</button>
          {group.status !== 'open' && (
            <button onClick={() => triage.mutate('open')} style={{ height: 34, padding: '0 13px', border: '1px solid var(--border)', background: 'var(--s2)', color: 'var(--text)', borderRadius: 8, fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}>Reopen</button>
          )}
        </div>
      </div>

      {latest && <LatestEvent event={latest} />}

      <div style={{ ...cardStyle, padding: 0, overflow: 'hidden', marginTop: 16 }}>
        <div style={{ padding: '13px 16px', borderBottom: '1px solid var(--border)' }}><h2 style={{ margin: 0, fontSize: 14, fontWeight: 700 }}>Event stream</h2></div>
        {events.map((e) => (
          <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderBottom: '1px solid var(--border)' }}>
            <span style={{ flex: 'none', width: 74, fontSize: 12, color: 'var(--muted)' }}>{relativeTime(e.occurred_at)}</span>
            <span style={{ flex: 1, minWidth: 0, fontSize: 12.5, fontFamily: 'var(--mono)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', color: 'var(--text)' }}>{e.message}</span>
            {e.release && <span style={{ flex: 'none', fontSize: 11, fontFamily: 'var(--mono)', color: 'var(--subtle)' }}>{e.release}</span>}
          </div>
        ))}
        {q.hasNextPage ? (
          <div style={{ padding: '12px 16px', textAlign: 'center' }}>
            <button onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage} style={ghostButton}>
              {q.isFetchingNextPage ? 'Loading…' : 'Load older events'}
            </button>
          </div>
        ) : (
          events.length > 0 && (
            <div style={{ padding: '12px 16px', textAlign: 'center', fontSize: 12.5, color: 'var(--muted)' }}>
              Showing all {events.length} event{events.length === 1 ? '' : 's'}.
            </div>
          )
        )}
      </div>
    </div>
  )
}

function LatestEvent({ event }: { event: CrashEvent }) {
  const tags: [string, string][] = []
  if (event.environment) tags.push(['env', event.environment])
  if (event.release) tags.push(['release', event.release])
  if (event.context) {
    for (const [k, v] of Object.entries(event.context)) {
      if (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') tags.push([k, String(v)])
    }
  }
  return (
    <div style={{ ...cardStyle, padding: 0, overflow: 'hidden' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '13px 16px', borderBottom: '1px solid var(--border)' }}>
        <h2 style={{ margin: 0, fontSize: 14, fontWeight: 700 }}>Latest event</h2>
        <span style={{ fontSize: 12, color: 'var(--muted)' }}>{relativeTime(event.occurred_at)}</span>
      </div>
      <div style={{ padding: 16 }}>
        {tags.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 14 }}>
            {tags.map(([k, v]) => (
              <span key={k} style={{ fontSize: 11.5, padding: '4px 10px', borderRadius: 7, background: 'var(--s2)', border: '1px solid var(--border)', fontFamily: 'var(--mono)' }}>
                <span style={{ color: 'var(--subtle)' }}>{k}</span> {v}
              </span>
            ))}
          </div>
        )}
        {event.stack ? (
          <>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--subtle)', marginBottom: 6 }}>Stack trace</div>
            <pre style={{ margin: 0, fontFamily: 'var(--mono)', fontSize: 11.5, lineHeight: 1.7, background: 'var(--s2)', border: '1px solid var(--border)', borderRadius: 8, padding: 14, overflow: 'auto', color: 'var(--text)', whiteSpace: 'pre' }}>{event.stack}</pre>
          </>
        ) : (
          <div style={{ fontSize: 13, color: 'var(--muted)' }}>{event.message}</div>
        )}
      </div>
    </div>
  )
}
