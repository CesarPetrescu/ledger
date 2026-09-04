import { useState, type FormEvent } from 'react'
import { ApiError } from '../api'
import { useAuth, type AuthNotice } from '../auth'
import { Icon } from '../components/ui'

const NOTICES: Record<AuthNotice, string> = {
  expired: 'Your session expired. Sign in again.',
  'signed-out': 'Signed out.',
}

function describeFailure(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return 'Password not accepted.'
    if (error.status === 429) return 'Too many attempts. Try again in a few minutes.'
    if (error.status === 403) return 'Sign-in was blocked. Open the console through its public address.'
  }
  return 'Sign-in failed. Try again.'
}

export function LoginPage({ notice }: { notice?: AuthNotice | undefined }) {
  const { signIn } = useAuth()
  const [password, setPassword] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await signIn(password)
    } catch (failure) {
      setError(describeFailure(failure))
      setBusy(false)
    }
  }

  return (
    <main className="login">
      <form className="login-panel" onSubmit={(event) => void submit(event)} aria-labelledby="login-title">
        <div className="brand">
          <span className="wordmark">Ledger</span>
          <span className="brand-sub">admin</span>
        </div>
        <h1 id="login-title">Operator sign-in</h1>
        {notice && <p className="notice">{NOTICES[notice]}</p>}
        <label htmlFor="password">Password</label>
        <div className="field-row">
          <input id="password" name="password" type={reveal ? 'text' : 'password'} autoComplete="current-password" required autoFocus value={password} onChange={(event) => setPassword(event.target.value)} disabled={busy} />
          <button type="button" className="icon-button" aria-label={reveal ? 'Hide password' : 'Show password'} aria-pressed={reveal} onClick={() => setReveal((value) => !value)}>
            <Icon name={reveal ? 'eye-off' : 'eye'} />
          </button>
        </div>
        {error && (
          <p className="field-error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </main>
  )
}
