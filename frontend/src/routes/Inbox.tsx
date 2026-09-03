import { useState } from 'react'
import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useLocation, useNavigate } from 'react-router-dom'
import * as api from '@/api/endpoints'
import type { ReportFilters } from '@/api/endpoints'
import { qk } from '@/api/keys'
import { paths } from '@/app/routes'
import { FilterChip, KindChip, StateBlock, StateChip, cardStyle, ghostButton, primaryButton } from '@/components/ui'
import { relativeTime } from '@/lib/format'
import type { ReportKind, ReportState, ReportSummary } from '@/api/types'

const STATES: ReportState[] = ['new', 'open', 'resolved', 'declined']
const KINDS: ReportKind[] = ['bug', 'idea', 'other']

export function Inbox() {
  const nav = useNavigate()
  // The filter a badge arrived with — an unread count is a count of something,
  // and landing on a list that is not that something leaves the reader to
  // rebuild it. Read at mount only: from here on the chips own the filter, and
  // re-reading would undo a click every time the router re-rendered.
  //
  // ⚠ Validated rather than trusted. `location.state` survives a reload from the
  // history entry and is whatever was last put there; an unrecognised state name
  // would activate no chip while still filtering the query, leaving a list the
  // toolbar says is unfiltered.
  const seed = (useLocation().state ?? {}) as { state?: string; site?: string }
  const [state, setState] = useState<ReportState | null>(
    STATES.includes(seed.state as ReportState) ? (seed.state as ReportState) : null,
  )
  const [site, setSite] = useState<string>(seed.site ?? '')
  const [kind, setKind] = useState<ReportKind | ''>('')

  const filters: ReportFilters = {
    ...(state ? { state } : {}),
    ...(site ? { site } : {}),
    ...(kind ? { kind } : {}),
  }

  const q = useInfiniteQuery({
    queryKey: qk.reports(filters),
    queryFn: ({ pageParam }) => api.listReports({ ...filters, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    // ⚠ Changing a filter changes the query key, and without this the data for
    // the new key is `undefined` until it arrives. The filter toolbar is gated on
    // there being reports, so it would unmount under the pointer that had just
    // clicked one of its chips — and a keyboard user's focus with it.
    placeholderData: keepPreviousData,
  })
  // The site filter is a list of the sites that exist, not free text: a typo
  // returning an empty page reads as "this site has no reports".
  const sitesQ = useQuery({ queryKey: qk.sites(), queryFn: () => api.listSites(), staleTime: 60_000 })

  const reports = q.data?.pages.flatMap((p) => p.items) ?? []
  const filtered = state !== null || site !== '' || kind !== ''
  // `stale` is "these rows belong to the filter you just left". They stay on
  // screen, dimmed, rather than being replaced by skeletons.
  const stale = q.isPlaceholderData
  const loading = q.isLoading || (stale && reports.length === 0)

  return (
    <div>
      <div style={{ marginBottom: 6 }}>
        <h1 style={{ margin: '0 0 4px', fontSize: 24, fontWeight: 800, letterSpacing: '-.01em' }}>Inbox</h1>
        <p style={{ margin: 0, fontSize: 12.5, color: 'var(--muted)' }}>
          Everything people wrote from inside the apps, newest first.
        </p>
      </div>

      {(reports.length > 0 || filtered || stale) && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', margin: '18px 0' }}>
          <FilterChip label="All" active={state === null} onClick={() => setState(null)} />
          {STATES.map((s) => (
            <FilterChip key={s} label={s} active={state === s} onClick={() => setState(state === s ? null : s)} />
          ))}
          <div style={{ flex: 1 }} />
          <select
            aria-label="Filter by site"
            value={site}
            onChange={(e) => setSite(e.target.value)}
            style={selectStyle}
          >
            <option value="">All sites</option>
            {(sitesQ.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
          <select
            aria-label="Filter by kind"
            value={kind}
            onChange={(e) => setKind(e.target.value as ReportKind | '')}
            style={selectStyle}
          >
            <option value="">All kinds</option>
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </div>
      )}

      {loading && <Skeletons />}

      {/* ⚠ Only when THIS query returned nothing of its own. A failed
          `fetchNextPage` also sets `isError`, with every earlier page still in
          `q.data` — so this block used to render on top of rows that had loaded,
          saying the request "failed before any page of reports came back". That
          page's failure is reported on its own button instead. `stale` still
          counts as nothing: those rows belong to the filter you just left. */}
      {q.isError && (stale || reports.length === 0) && (
        <StateBlock
          icon="⚠"
          danger
          title="Couldn't load the inbox"
          body="The request failed before any page of reports came back. Nothing has been lost — reports are stored the moment they arrive."
        >
          <button onClick={() => void q.refetch()} style={ghostButton}>
            Retry
          </button>
        </StateBlock>
      )}

      {!loading && !q.isError && reports.length === 0 && !filtered && (
        <StateBlock
          icon="✉"
          title="No reports yet"
          body="Nothing has been written from inside the apps. Turn feedback on for a site and paste its embed snippet — the first report shows up here the moment someone sends one."
        >
          <button onClick={() => nav(paths.board)} style={primaryButton}>
            Pick a site to enable
          </button>
        </StateBlock>
      )}

      {!loading && !q.isError && reports.length === 0 && filtered && (
        <StateBlock icon="✉" title="Nothing matches" body="No report has this state, site and kind together.">
          <button
            onClick={() => {
              setState(null)
              setSite('')
              setKind('')
            }}
            style={ghostButton}
          >
            Clear filters
          </button>
        </StateBlock>
      )}

      {reports.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 9, opacity: stale ? 0.55 : 1, transition: 'opacity .12s ease' }}>
          {reports.map((r) => (
            <Row key={r.ref} report={r} onClick={() => nav(paths.report(r.ref))} />
          ))}
          {q.hasNextPage && (
            <div style={{ padding: '6px 0 0', textAlign: 'center' }}>
              <button onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage || stale} style={ghostButton}>
                {q.isFetchingNextPage ? 'Loading…' : q.isError ? "That page didn't load — try again" : 'Load older reports'}
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

const selectStyle = {
  height: 30,
  padding: '0 8px',
  borderRadius: 8,
  fontSize: 12.5,
  fontWeight: 600,
  border: '1px solid var(--border)',
  background: 'var(--s2)',
  color: 'var(--text)',
  fontFamily: 'inherit',
  cursor: 'pointer',
} as const

function Row({ report, onClick }: { report: ReportSummary; onClick: () => void }) {
  const fresh = report.state === 'new'
  const closed = report.state === 'resolved' || report.state === 'declined'
  const firstLine = report.message.split('\n')[0]
  return (
    <button
      onClick={onClick}
      // The accessible name leads with the state, so a screen-reader user hears
      // the triage state before the text of the report.
      aria-label={`${report.state}, ${report.kind}, ${report.site_id} — ${firstLine}`}
      style={{
        ...cardStyle,
        padding: '13px 15px',
        display: 'block',
        width: '100%',
        textAlign: 'left',
        cursor: 'pointer',
        fontFamily: 'inherit',
        color: 'var(--text)',
        border: `1px solid ${fresh ? 'color-mix(in oklab, var(--accent) 38%, var(--border))' : 'var(--border)'}`,
        opacity: closed ? 0.78 : 1,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 9, flexWrap: 'wrap' }}>
        <StateChip state={report.state} />
        <KindChip kind={report.kind} />
        <span style={{ fontFamily: 'var(--mono)', fontSize: 11.5, color: 'var(--subtle)' }}>{report.site_id}</span>
        <div style={{ flex: 1 }} />
        {report.attachment_count > 0 && (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 11.5, color: 'var(--muted)' }}>
            <span aria-hidden>📎</span>
            {report.attachment_count}
          </span>
        )}
        <span style={{ fontSize: 11.5, color: 'var(--subtle)', whiteSpace: 'nowrap' }}>
          {relativeTime(report.created_at)}
        </span>
        <span aria-hidden style={{ color: 'var(--subtle)' }}>
          ›
        </span>
      </div>
      <div
        style={{
          marginTop: 9,
          fontSize: 13.5,
          fontWeight: fresh ? 600 : 500,
          whiteSpace: 'nowrap',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
        }}
      >
        {firstLine}
      </div>
      <div style={{ marginTop: 6, display: 'flex', alignItems: 'center', gap: 10, fontSize: 12, color: 'var(--muted)', flexWrap: 'wrap' }}>
        <span>{report.reporter_label ?? 'no name given'}</span>
        <span style={{ opacity: 0.4 }}>·</span>
        <span style={{ fontFamily: 'var(--mono)', fontSize: 11.5, color: 'var(--subtle)' }}>{report.ref}</span>
      </div>
    </button>
  )
}

function Skeletons() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 9, marginTop: 18 }}>
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} style={{ border: '1px solid var(--border)', background: 'var(--s1)', borderRadius: 10, padding: '14px 15px' }}>
          <div style={{ display: 'flex', gap: 9, alignItems: 'center' }}>
            <div className="om-skel" style={{ height: 19, width: 64, borderRadius: 999 }} />
            <div className="om-skel" style={{ height: 19, width: 52, borderRadius: 6 }} />
            <div style={{ flex: 1 }} />
            <div className="om-skel" style={{ height: 11, width: 60 }} />
          </div>
          <div className="om-skel" style={{ height: 13, width: '72%', marginTop: 12 }} />
          <div className="om-skel" style={{ height: 11, width: '30%', marginTop: 9 }} />
        </div>
      ))}
    </div>
  )
}
