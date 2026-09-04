import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
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
  const sessionGeneration = useRef(0)

  useEffect(() => {
    let active = true
    api.getSession().then(
      (session) => {
        if (!active) return
        sessionGeneration.current += 1
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
        sessionGeneration.current += 1
        setCsrfToken(null)
        setState((previous) => (previous.status === 'authenticated' ? { status: 'anonymous', notice: 'expired' } : previous))
      }),
    [],
  )

  useEffect(() => {
    if (state.status !== 'authenticated') return
    const generation = sessionGeneration.current
    const expiresAt = state.expiresAt
    const parsed = Date.parse(expiresAt)
    const delay = Number.isNaN(parsed) ? 0 : Math.max(0, parsed - Date.now())
    const timer = window.setTimeout(() => {
      if (sessionGeneration.current !== generation) return
      sessionGeneration.current += 1
      setCsrfToken(null)
      setState({ status: 'anonymous', notice: 'expired' })
    }, delay)
    return () => window.clearTimeout(timer)
  }, [state])

  const signIn = useCallback(async (password: string) => {
    const session = await api.login(password)
    sessionGeneration.current += 1
    setCsrfToken(session.csrf_token)
    setState({ status: 'authenticated', expiresAt: session.expires_at })
  }, [])

  const signOut = useCallback(async () => {
    await api.logout()
    sessionGeneration.current += 1
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
