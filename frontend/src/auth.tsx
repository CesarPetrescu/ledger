import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, onUnauthorized, setCsrfToken } from './api'

export type AuthNotice = 'expired' | 'signed-out'

export type AuthState = { status: 'loading' } | { status: 'anonymous'; notice?: AuthNotice } | { status: 'authenticated'; expiresAt: string }

interface AuthContextValue {
  state: AuthState
  signIn: (password: string) => Promise<void>
  signOut: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: 'loading' })

  useEffect(() => {
    let active = true
    api.getSession().then(
      (session) => {
        if (!active) return
        setCsrfToken(session.csrf_token)
        setState({ status: 'authenticated', expiresAt: session.expires_at })
      },
      () => {
        if (active) setState({ status: 'anonymous' })
      },
    )
    return () => {
      active = false
    }
  }, [])

  useEffect(
    () =>
      onUnauthorized(() => {
        setCsrfToken(null)
        setState((previous) => (previous.status === 'authenticated' ? { status: 'anonymous', notice: 'expired' } : previous))
      }),
    [],
  )

  const signIn = useCallback(async (password: string) => {
    const session = await api.login(password)
    setCsrfToken(session.csrf_token)
    setState({ status: 'authenticated', expiresAt: session.expires_at })
  }, [])

  const signOut = useCallback(async () => {
    await api.logout()
    setCsrfToken(null)
    setState({ status: 'anonymous', notice: 'signed-out' })
  }, [])

  const value = useMemo(() => ({ state, signIn, signOut }), [state, signIn, signOut])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth requires AuthProvider')
  return value
}
