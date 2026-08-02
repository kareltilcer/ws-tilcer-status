import { useState, type CSSProperties, type FormEvent } from 'react'
import * as api from '@/api/endpoints'
import { ApiError } from '@/api/client'
import type { UserPublic } from '@/api/types'
import { Spinner } from './ui'

export function LoginScreen({ onSuccess }: { onSuccess: (u: UserPublic) => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setLoading(true)
    setError(null)
    try {
      const { user } = await api.login(email, password)
      onSuccess(user)
    } catch (err) {
      const ae = err as ApiError
      setError(
        ae?.code === 'mfa_required'
          ? 'Complete sign-in at auth.tilcer.cz, then return here.'
          : ae?.code === 'auth_unreachable'
            ? 'The auth service is unavailable. Try again shortly.'
            : 'Incorrect email or password. Try again.',
      )
    } finally {
      setLoading(false)
    }
  }

  const field: CSSProperties = {
    height: 40,
    width: '100%',
    border: '1px solid var(--border)',
    background: 'var(--s2)',
    borderRadius: 8,
    padding: '0 12px',
    fontSize: 14,
    color: 'var(--text)',
    fontFamily: 'inherit',
    outline: 'none',
  }

  return (
    <div style={{ display: 'grid', placeItems: 'center', minHeight: '100vh', padding: 24, background: 'var(--bg)' }}>
      <div style={{ width: '100%', maxWidth: 380, animation: 'om-fadein .25s ease' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 22 }}>
          <span style={{ display: 'grid', placeItems: 'center', height: 36, width: 36, borderRadius: 9, background: 'var(--accent)', color: 'var(--accent-fg)', fontWeight: 800, fontSize: 18 }}>s</span>
          <span style={{ fontSize: 20, fontWeight: 800, letterSpacing: '-.01em' }}>status</span>
        </div>
        <form onSubmit={submit} style={{ border: '1px solid var(--border)', background: 'var(--s1)', borderRadius: 14, padding: 22, boxShadow: 'var(--shadow)' }}>
          <h1 style={{ margin: '0 0 4px', fontSize: 18, fontWeight: 700 }}>Sign in</h1>
          <p style={{ margin: '0 0 18px', fontSize: 13, color: 'var(--muted)' }}>Fleet monitoring &amp; crash reporting. Admin only.</p>
          {error && (
            <div role="alert" style={{ marginBottom: 16, border: '1px solid color-mix(in oklab, var(--danger) 45%, transparent)', background: 'color-mix(in oklab, var(--danger) 12%, transparent)', color: 'var(--danger-text)', borderRadius: 9, padding: '10px 12px', fontSize: 13 }}>
              {error}
            </div>
          )}
          <label style={{ display: 'block', marginBottom: 14 }}>
            <span style={{ display: 'block', marginBottom: 6, fontSize: 13, fontWeight: 600, color: 'var(--subtle)' }}>Email</span>
            <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} style={field} />
          </label>
          <label style={{ display: 'block', marginBottom: 18 }}>
            <span style={{ display: 'block', marginBottom: 6, fontSize: 13, fontWeight: 600, color: 'var(--subtle)' }}>Password</span>
            <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} style={field} />
          </label>
          <button type="submit" disabled={loading} style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8, height: 40, width: '100%', border: 'none', borderRadius: 8, background: 'var(--accent)', color: 'var(--accent-fg)', fontSize: 14, fontWeight: 700, cursor: 'pointer' }}>
            {loading && <Spinner />}
            <span>{loading ? 'Signing in…' : 'Sign in'}</span>
          </button>
          <div style={{ marginTop: 16, textAlign: 'center', fontSize: 12.5, color: 'var(--muted)' }}>
            <p style={{ margin: '4px 0 0' }}>Accounts are managed in auth.tilcer.cz.</p>
          </div>
        </form>
      </div>
    </div>
  )
}
