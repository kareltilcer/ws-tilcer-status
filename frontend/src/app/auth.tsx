import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import * as api from '@/api/endpoints'
import { setUnauthorizedHandler } from '@/api/client'
import type { UserPublic } from '@/api/types'
import { LoginScreen } from '@/components/LoginScreen'

// Auth identity for the SPA (Mode B). The browser holds NO token — the app learns
// who it is from GET /api/auth/session, and every request is authorized by the
// session cookie.
export interface Identity {
  userId: string
  email: string
  label: string
  roles: string[]
}

interface AuthContextValue {
  identity: Identity
  isAdmin: boolean
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

function hasRole(roles: string[], ...allowed: string[]): boolean {
  return roles.some((r) => r === '*' || allowed.includes(r))
}

function toIdentity(u: UserPublic): Identity {
  return { userId: u.id, email: u.email, label: u.display_name || u.email, roles: u.roles }
}

type State = { status: 'loading' } | { status: 'anon' } | { status: 'auth'; identity: Identity }

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>({ status: 'loading' })
  const qc = useQueryClient()

  const resolve = useCallback(async () => {
    try {
      const { user } = await api.getSession()
      setState({ status: 'auth', identity: toIdentity(user) })
    } catch {
      setState({ status: 'anon' })
    }
  }, [])

  useEffect(() => {
    // Any 401 mid-session drops the app to the login screen and clears cached data.
    setUnauthorizedHandler(() => {
      qc.clear()
      setState({ status: 'anon' })
    })
    void resolve()
    return () => setUnauthorizedHandler(null)
  }, [resolve, qc])

  if (state.status === 'loading') {
    return <div style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', color: 'var(--muted)' }}>Loading…</div>
  }
  if (state.status === 'anon') {
    return <LoginScreen onSuccess={(user) => setState({ status: 'auth', identity: toIdentity(user) })} />
  }

  const identity = state.identity
  const logout = () => {
    void api.logout().finally(() => {
      qc.clear()
      setState({ status: 'anon' })
    })
  }

  const value: AuthContextValue = {
    identity,
    isAdmin: hasRole(identity.roles, 'admin'),
    logout,
  }
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
