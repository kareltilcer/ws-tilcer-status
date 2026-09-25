import { useState, type CSSProperties } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import * as api from '@/api/endpoints'
import { qk } from '@/api/keys'
import { useAuth } from '@/app/auth'
import { useTheme } from '@/theme/theme'
import { useMediaQuery } from '@/lib/useMediaQuery'
import { paths } from '@/app/routes'

function navItemStyle(active: boolean): CSSProperties {
  return {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    height: 38,
    padding: '0 12px',
    borderRadius: 8,
    border: 'none',
    background: active ? 'var(--s3)' : 'transparent',
    color: active ? 'var(--text)' : 'var(--muted)',
    fontSize: 13.5,
    fontWeight: 600,
    cursor: 'pointer',
    textAlign: 'left',
    width: '100%',
    fontFamily: 'inherit',
  }
}

export function AppShell() {
  const { identity, logout } = useAuth()
  const { theme, toggle } = useTheme()
  const nav = useNavigate()
  const loc = useLocation()
  const isMobile = useMediaQuery('(max-width: 720px)')
  const [drawer, setDrawer] = useState(false)
  const themeGlyph = theme === 'dark' ? '☾' : '☀'
  const onBoard = loc.pathname === paths.board
  const onReports = loc.pathname.startsWith(paths.reports)

  // The nav's unread count. It shares the board's query — one request, two
  // readers — and stays absent rather than showing a zero when the feedback
  // module is not composed into this deployment (V3-D53).
  //
  // ⚠ It carries its own `refetchInterval`. The Board sets one, but the shell
  // outlives the Board: leave that route and the shared query stops polling, so
  // the badge sits on a number from whenever the user last looked. A nav badge
  // that silently goes stale is worse than no badge.
  //
  // ⚠ And only while the Board is NOT mounted. An interval is per observer, not
  // per query, so two of them on the same key sit at different phases and poll
  // /api/sites about twice per 30 s — double the rate the Board's own header
  // promises, against a service whose single writer connection every request
  // has to queue behind.
  const { data: sites } = useQuery({
    queryKey: qk.sites(),
    queryFn: () => api.listSites(),
    staleTime: 30_000,
    refetchInterval: onBoard ? false : 30_000,
  })
  const unread = (sites ?? []).reduce((n, s) => n + (s.open_reports ?? 0), 0)

  const logoMark = (size: number) => (
    <span style={{ display: 'grid', placeItems: 'center', height: size, width: size, borderRadius: size / 4, background: 'var(--accent)', color: 'var(--accent-fg)', fontWeight: 800, fontSize: size / 2 }}>s</span>
  )

  const navContent = (
    <>
      <nav style={{ display: 'flex', flexDirection: 'column', gap: 2, padding: '6px 12px', flex: 1 }}>
        <button onClick={() => { nav(paths.board); setDrawer(false) }} style={navItemStyle(onBoard)}>Board</button>
        <button onClick={() => { nav(paths.reports); setDrawer(false) }} style={navItemStyle(onReports)}>
          <span style={{ flex: 1 }}>Inbox</span>
          {unread > 0 && (
            // The number alone reads as "Inbox 3", which says nothing about what
            // 3 counts. `role="img"` + a label names it — a bare aria-label on a
            // generic span is not required to be exposed at all — and the digits
            // become the image's content rather than a second reading of it.
            // The board's UnreadBadge spells this out in words; here there is
            // room for two glyphs, so the name carries what the pill cannot.
            //
            // ⚠ "new", not "open". `open_reports` counts reports in state `new`,
            // and `open` is a DIFFERENT state in the same enum — the second chip
            // in the inbox filter, and the one triage moves a report to in order
            // to CLEAR this badge. A screen-reader user told "3 open reports"
            // filtered by `open`, saw zero rows, and had nothing to reconcile the
            // two with. UnreadBadge and ReportDetail both say "new"; so does this.
            <span
              role="img"
              aria-label={`${unread} new ${unread === 1 ? 'report' : 'reports'}`}
              style={{ display: 'inline-grid', placeItems: 'center', minWidth: 20, height: 20, padding: '0 6px', borderRadius: 999, background: 'var(--accent)', color: 'var(--accent-fg)', fontSize: 11, fontWeight: 800, fontVariantNumeric: 'tabular-nums' }}
            >
              {unread}
            </span>
          )}
        </button>
        <button onClick={() => { nav(paths.addSite); setDrawer(false) }} style={navItemStyle(loc.pathname === paths.addSite)}>Add site</button>
        <button onClick={() => { nav(paths.notifications); setDrawer(false) }} style={navItemStyle(loc.pathname === paths.notifications)}>Notifications</button>
      </nav>
      <div style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
        <div style={{ padding: '0 4px', fontSize: 12, color: 'var(--muted)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{identity.email}</div>
        <button onClick={toggle} style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8, height: 36, border: '1px solid var(--border)', background: 'var(--s2)', borderRadius: 8, color: 'var(--text)', fontSize: 13, fontWeight: 600, cursor: 'pointer' }}>{themeGlyph}<span>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</span></button>
        <button onClick={logout} style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8, height: 36, border: '1px solid var(--border)', background: 'var(--s2)', borderRadius: 8, color: 'var(--text)', fontSize: 13, fontWeight: 600, cursor: 'pointer' }}>Sign out</button>
      </div>
    </>
  )

  const content = (
    <main style={{ flex: 1, minWidth: 0, overflow: 'auto' }}>
      <div style={{ maxWidth: 1180, margin: '0 auto', padding: isMobile ? '18px 14px 48px' : '28px 28px 64px' }}>
        <Outlet />
      </div>
    </main>
  )

  if (isMobile) {
    return (
      <div style={{ position: 'relative', minHeight: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, height: 52, padding: '0 12px', borderBottom: '1px solid var(--border)', background: 'var(--s1)', flex: 'none', zIndex: 5 }}>
          <button onClick={() => setDrawer(true)} aria-label="Menu" style={{ height: 38, width: 38, display: 'grid', placeItems: 'center', background: 'transparent', border: '1px solid var(--border)', borderRadius: 8, color: 'var(--text)', cursor: 'pointer' }}>☰</button>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>{logoMark(26)}<span style={{ fontSize: 15, fontWeight: 800, letterSpacing: '-.01em' }}>status</span></div>
          <div style={{ flex: 1 }} />
          <button onClick={() => nav(paths.addSite)} aria-label="Add site" style={{ height: 38, width: 38, display: 'grid', placeItems: 'center', background: 'var(--accent)', border: '1px solid var(--accent)', borderRadius: 8, color: 'var(--accent-fg)', cursor: 'pointer' }}>+</button>
        </div>
        {drawer && (
          <>
            <div onClick={() => setDrawer(false)} style={{ position: 'fixed', inset: 0, background: 'oklch(0 0 0 / .45)', zIndex: 30 }} />
            <div style={{ position: 'fixed', top: 0, left: 0, bottom: 0, width: 220, zIndex: 31, background: 'var(--s1)', borderRight: '1px solid var(--border)', display: 'flex', flexDirection: 'column', animation: 'om-slidein .2s ease' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: 16 }}>{logoMark(30)}<span style={{ fontSize: 16, fontWeight: 800 }}>status</span></div>
              {navContent}
            </div>
          </>
        )}
        {content}
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--bg)' }}>
      <aside style={{ width: 230, flex: 'none', display: 'flex', flexDirection: 'column', borderRight: '1px solid var(--border)', background: 'var(--s1)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: 18 }}>{logoMark(32)}<span style={{ fontSize: 17, fontWeight: 800, letterSpacing: '-.01em' }}>status</span></div>
        {navContent}
      </aside>
      {content}
    </div>
  )
}
