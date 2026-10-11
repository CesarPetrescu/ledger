import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, onUnauthorized, setCsrfToken, type PasskeyRequired, type Session } from './api'
import type { CredentialJSON } from './webauthn'

export type AuthNotice = 'expired' | 'signed-out'

export type AuthState = { status: 'loading' } | { status: 'anonymous'; notice?: AuthNotice } | { status: 'authenticated'; expiresAt: string; remember: boolean }

interface AuthContextValue {
  state: AuthState
  /** Resolves null once signed in, or with the passkey step when the password alone is not enough. */
  signIn: (password: string, remember: boolean) => Promise<PasskeyRequired | null>
  signInWithPasskey: (proof: { challenge_id: string } | { pending: string }, credential: CredentialJSON, remember: boolean) => Promise<void>
  signOut: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: 'loading' })
  const sessionGeneration = useRef(0)

  const accept = useCallback((session: Session) => {
    sessionGeneration.current += 1
    setCsrfToken(session.csrf_token)
    setState({ status: 'authenticated', expiresAt: session.expires_at, remember: session.remember === true })
  }, [])

  useEffect(() => {
    let active = true
    api.getSession().then(
      (session) => {
        if (active) accept(session)
      },
      () => {
        if (active) setState({ status: 'anonymous' })
      },
    )
    return () => {
      active = false
    }
  }, [accept])

  useEffect(
    () =>
      onUnauthorized(() => {
        sessionGeneration.current += 1
        setCsrfToken(null)
        setState((previous) => (previous.status === 'authenticated' ? { status: 'anonymous', notice: 'expired' } : previous))
      }),
    [],
  )

  // A remembered session slides forward on the server with every use, and its 30 days would overflow
  // setTimeout anyway, so only the server ends it: the next request answers 401.
  useEffect(() => {
    if (state.status !== 'authenticated' || state.remember) return
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

  const signIn = useCallback(async (password: string, remember: boolean) => {
    const result = await api.login(password, remember)
    if ('passkey_required' in result) return result
    accept(result)
    return null
  }, [accept])

  const signInWithPasskey = useCallback(async (proof: { challenge_id: string } | { pending: string }, credential: CredentialJSON, remember: boolean) => {
    accept(await api.loginWithPasskey(proof, credential, remember))
  }, [accept])

  const signOut = useCallback(async () => {
    await api.logout()
    sessionGeneration.current += 1
    setCsrfToken(null)
    setState({ status: 'anonymous', notice: 'signed-out' })
  }, [])

  const value = useMemo(() => ({ state, signIn, signInWithPasskey, signOut }), [state, signIn, signInWithPasskey, signOut])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth requires AuthProvider')
  return value
}
