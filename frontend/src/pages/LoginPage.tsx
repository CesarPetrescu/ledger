import { useState, type FormEvent, type KeyboardEvent } from 'react'
import { ApiError } from '../api'
import { useAuth, type AuthNotice } from '../auth'
import { BrandMark, Icon } from '../components/ui'

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

const POINTS = [
  { icon: 'inbox' as const, title: 'One inbox', text: 'Questions from every agent, answered in place.' },
  { icon: 'projects' as const, title: 'Shared memory', text: 'Decisions, todos, and history agents read before they act.' },
  { icon: 'handoffs' as const, title: 'Handoffs', text: 'Pass work between agents with the whole context.' },
]

export function LoginPage({ notice }: { notice?: AuthNotice | undefined }) {
  const { signIn } = useAuth()
  const [password, setPassword] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [capsLock, setCapsLock] = useState(false)

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
  const watchCaps = (event: KeyboardEvent<HTMLInputElement>) => setCapsLock(event.getModifierState?.('CapsLock') ?? false)

  return (
    <main className="login">
      <section className="login-hero" aria-label="About Ledger">
        <div className="login-glow" aria-hidden="true" />
        <div className="brand login-brand">
          <BrandMark size={40} />
          <span className="wordmark">Ledger</span>
        </div>
        <div className="login-pitch">
          <h1>Memory your agents share. A desk where you answer them.</h1>
          <ul className="login-points">
            {POINTS.map((point) => (
              <li key={point.title}>
                <span className="login-point-icon"><Icon name={point.icon} /></span>
                <span><strong>{point.title}</strong> {point.text}</span>
              </li>
            ))}
          </ul>
        </div>
        {/* A glimpse of the Inbox; decoration only. */}
        <div className="login-preview" aria-hidden="true">
          <div className="login-preview-card" data-tone="ask">
            <span className="login-preview-tag">claude-code asks you</span>
            <strong>Confirm the pricing before launch</strong>
            <span className="login-preview-reply">Answer claude-code…</span>
          </div>
          <div className="login-preview-card">
            <span className="login-preview-tag">codex · decision</span>
            <strong>Use Stripe for billing</strong>
          </div>
          <div className="login-preview-card">
            <span className="login-preview-tag">todo · due tomorrow</span>
            <strong>Write the launch checklist</strong>
          </div>
        </div>
      </section>

      <section className="login-side">
        <form className="login-card" onSubmit={(event) => void submit(event)} aria-labelledby="login-title">
          <div className="login-card-brand"><BrandMark size={44} /></div>
          <p className="eyebrow">Operator sign-in</p>
          <h2 id="login-title">Welcome back</h2>
          <p className="muted">Sign in with your owner password to reach your projects, inbox, and agents.</p>
          {notice && <p className="notice">{NOTICES[notice]}</p>}
          <label htmlFor="password">Password</label>
          <div className="login-field">
            <input id="password" name="password" type={reveal ? 'text' : 'password'} autoComplete="current-password" required autoFocus value={password}
              onChange={(event) => setPassword(event.target.value)} onKeyUp={watchCaps} onKeyDown={watchCaps} disabled={busy}
              aria-invalid={error ? true : undefined} aria-describedby={capsLock ? 'caps-lock' : undefined} />
            <button type="button" className="login-reveal" aria-label={reveal ? 'Hide password' : 'Show password'} aria-pressed={reveal} onClick={() => setReveal((value) => !value)}>
              <Icon name={reveal ? 'eye-off' : 'eye'} />
            </button>
          </div>
          {capsLock && <p id="caps-lock" className="login-caps">Caps Lock is on.</p>}
          {error && <p className="field-error" role="alert">{error}</p>}
          <button type="submit" className="btn btn-primary login-submit" disabled={busy}>
            {busy ? <><span className="login-spinner" aria-hidden="true" /> Signing in…</> : <>Sign in <Icon name="arrow" /></>}
          </button>
          <p className="login-foot"><Icon name="live" /> Private, self-hosted · <span>{window.location.host}</span></p>
        </form>
      </section>
    </main>
  )
}
