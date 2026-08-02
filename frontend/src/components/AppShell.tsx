import { useState, type CSSProperties } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
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

  const logoMark = (size: number) => (
    <span style={{ display: 'grid', placeItems: 'center', height: size, width: size, borderRadius: size / 4, background: 'var(--accent)', color: 'var(--accent-fg)', fontWeight: 800, fontSize: size / 2 }}>s</span>
  )

  const navContent = (
    <>
      <nav style={{ display: 'flex', flexDirection: 'column', gap: 2, padding: '6px 12px', flex: 1 }}>
        <button onClick={() => { nav(paths.board); setDrawer(false) }} style={navItemStyle(onBoard)}>Board</button>
        <button onClick={() => { nav(paths.addSite); setDrawer(false) }} style={navItemStyle(loc.pathname === paths.addSite)}>Add site</button>
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
