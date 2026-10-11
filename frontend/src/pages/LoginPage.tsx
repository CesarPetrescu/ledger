import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { api, ApiError, type PasskeyRequired } from '../api'
import { useAuth, type AuthNotice } from '../auth'
import { BrandMark, Icon } from '../components/ui'
import { getPasskey, passkeyCancelled, passkeysSupported, passkeyUsedHere, setPasskeyUsedHere } from '../webauthn'

const NOTICES: Record<AuthNotice, string> = {
  expired: 'Your session expired. Sign in again.',
  'signed-out': 'Signed out.',
}

function describeFailure(error: unknown, passkey: boolean): string {
  if (passkeyCancelled(error)) return 'The passkey prompt closed before you confirmed. Try again when you are ready.'
  if (error instanceof DOMException && error.name === 'SecurityError') return 'Passkeys do not work at this address. Open the console through its public address.'
  if (error instanceof ApiError) {
    if (error.status === 401) return passkey ? 'Passkey not accepted.' : 'Password not accepted.'
    if (error.status === 409) return 'No passkey is set up yet. Sign in with your password, then add one on Access.'
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
  const { signIn, signInWithPasskey } = useAuth()
  const [password, setPassword] = useState('')
  const [reveal, setReveal] = useState(false)
  const [remember, setRemember] = useState(true)
  const [busy, setBusy] = useState<'' | 'password' | 'passkey'>('')
  // Shown next to the control that failed.
  const [error, setError] = useState<{ text: string; at: 'password' | 'passkey' } | null>(null)
  const [capsLock, setCapsLock] = useState(false)
  // Set once the password was right but a passkey is registered: sign-in ends with a passkey.
  const [pending, setPending] = useState<PasskeyRequired | null>(null)
  const canPasskey = passkeysSupported()
  // The passkey is the primary button once this browser has used one; until then (and right after the
  // upgrade, when no passkey exists) the password is.
  const [passkeyFirst, setPasskeyFirst] = useState(() => canPasskey && passkeyUsedHere())
  const passwordInput = useRef<HTMLInputElement>(null)
  const passkeyButton = useRef<HTMLButtonElement>(null)

  // Every control is disabled while waiting, which drops focus to the page. After a failure, put it
  // back on the control that failed so Enter retries.
  useEffect(() => {
    if (error && !busy) (error.at === 'passkey' ? passkeyButton : passwordInput).current?.focus()
  }, [error, busy])

  const fail = (failure: unknown, at: 'password' | 'passkey', passkey = at === 'passkey') => {
    if (failure instanceof ApiError && failure.status === 409) {
      setPasskeyUsedHere(false)
      setPasskeyFirst(false)
    }
    setError({ text: describeFailure(failure, passkey), at })
    setBusy('')
  }

  const confirmPasskey = async (step: PasskeyRequired) => {
    setBusy('passkey')
    setError(null)
    let credential
    try {
      credential = await getPasskey(step.options)
    } catch (failure) {
      // Nothing reached the server, so the pending sign-in still holds: retry from here.
      fail(failure, 'passkey')
      return
    }
    try {
      await signInWithPasskey({ pending: step.pending }, credential, remember)
      setPasskeyUsedHere(true)
    } catch (failure) {
      // The pending sign-in is single use (and short-lived): start again from the password.
      setPending(null)
      fail(failure, 'password', true)
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy) return
    if (pending) return confirmPasskey(pending)
    setBusy('password')
    setError(null)
    try {
      const step = await signIn(password, remember)
      if (!step) return
      setPassword('')
      setReveal(false)
      setPending(step)
      await confirmPasskey(step)
    } catch (failure) {
      fail(failure, 'password')
    }
  }

  const signInWithPasskeyOnly = async () => {
    if (busy) return
    setBusy('passkey')
    setError(null)
    try {
      const { challenge_id, options } = await api.passkeyLoginOptions()
      const credential = await getPasskey(options)
      await signInWithPasskey({ challenge_id }, credential, remember)
      setPasskeyUsedHere(true)
    } catch (failure) {
      fail(failure, 'passkey')
    }
  }

  const backToPassword = () => {
    setPending(null)
    setError(null)
  }
  const watchCaps = (event: KeyboardEvent<HTMLInputElement>) => setCapsLock(event.getModifierState?.('CapsLock') ?? false)
  const errorAt = (at: 'password' | 'passkey') => error?.at === at && <p className="field-error" role="alert">{error.text}</p>
  const waiting = <><span className="login-spinner" aria-hidden="true" /> Waiting for your passkey…</>

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
          {pending ? <>
            <h2 id="login-title">Confirm with your passkey</h2>
            <p className="muted">Your password is right. Approve the passkey prompt from Bitwarden, your phone, or this device to finish signing in.</p>
            {errorAt('passkey')}
            <button ref={passkeyButton} type="submit" className="btn btn-primary login-submit" disabled={busy !== ''} autoFocus>
              {busy ? waiting : <><Icon name="passkey" /> Use passkey</>}
            </button>
            <button type="button" className="btn login-submit login-back" onClick={backToPassword} disabled={busy !== ''}>
              <Icon name="back" /> Back to password
            </button>
          </> : <>
            <h2 id="login-title">Welcome back</h2>
            <p className="muted login-intro">Sign in with a passkey or your owner password to reach your projects, inbox, and agents.</p>
            {notice && !error && <p className="notice">{NOTICES[notice]}</p>}
            {canPasskey && <>
              <button ref={passkeyButton} type="button" className={passkeyFirst ? 'btn btn-primary login-submit' : 'btn login-submit'} onClick={() => void signInWithPasskeyOnly()} disabled={busy !== ''}>
                {busy === 'passkey' ? waiting : <><Icon name="passkey" /> Sign in with a passkey</>}
              </button>
              {errorAt('passkey')}
              <p className="login-or">or use your password</p>
            </>}
            <label htmlFor="password">Password</label>
            <div className="login-field">
              <input ref={passwordInput} id="password" name="password" type={reveal ? 'text' : 'password'} autoComplete="current-password" required autoFocus value={password}
                onChange={(event) => setPassword(event.target.value)} onKeyUp={watchCaps} onKeyDown={watchCaps} disabled={busy !== ''}
                aria-invalid={error?.at === 'password' ? true : undefined} aria-describedby={capsLock ? 'caps-lock' : undefined} />
              <button type="button" className="login-reveal" aria-label={reveal ? 'Hide password' : 'Show password'} aria-pressed={reveal} onClick={() => setReveal((value) => !value)}>
                <Icon name={reveal ? 'eye-off' : 'eye'} />
              </button>
            </div>
            {capsLock && <p id="caps-lock" className="login-caps">Caps Lock is on.</p>}
            {errorAt('password')}
            <button type="submit" className={passkeyFirst ? 'btn login-submit' : 'btn btn-primary login-submit'} disabled={busy !== ''}>
              {busy === 'password' ? <><span className="login-spinner" aria-hidden="true" /> Signing in…</> : <>Sign in <Icon name="arrow" /></>}
            </button>
            <label className="check login-remember">
              <input type="checkbox" checked={remember} onChange={(event) => setRemember(event.target.checked)} disabled={busy !== ''} />
              <span>Remember this device for 30 days <span className="hint">Uncheck on a shared computer.</span></span>
            </label>
          </>}
          <p className="login-foot"><Icon name="live" /> Private, self-hosted · <span>{window.location.host}</span></p>
        </form>
      </section>
    </main>
  )
}
