import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { anonymousSession, authenticatedSession, futureSessionExpiry, homeRoutes, mockApi, renderApp, requestOptionsJSON, stubPasskeys } from './helpers'

describe('login', () => {
  it('shows a compact sign-in, without a passkey button where the browser has no passkeys', async () => {
    mockApi({ 'GET /admin/api/session': anonymousSession })
    renderApp()
    expect(await screen.findByRole('heading', { name: /welcome back/i })).toBeInTheDocument()
    expect(screen.getByText(/owner sign-in/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^password$/i)).toHaveAttribute('type', 'password')
    expect(screen.queryByLabelText(/username|email/i)).not.toBeInTheDocument()
    expect(document.querySelectorAll('input')).toHaveLength(2)
    // A personal console: remembered by default, with a hint for shared computers.
    expect(screen.getByRole('checkbox', { name: /remember this device for 30 days/i })).toBeChecked()
    expect(screen.getByText(/uncheck on a shared computer/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /passkey/i })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^sign in$/i })).toHaveClass('btn-primary')
    expect(screen.getByLabelText(/^password$/i)).toHaveFocus()
  })

  it('toggles password visibility without changing the value', async () => {
    mockApi({ 'GET /admin/api/session': anonymousSession })
    renderApp()
    const user = userEvent.setup()
    const field = await screen.findByLabelText(/^password$/i)
    await user.type(field, 'hunter2')
    await user.click(screen.getByRole('button', { name: /show password/i }))
    expect(field).toHaveAttribute('type', 'text')
    expect(field).toHaveValue('hunter2')
    await user.click(screen.getByRole('button', { name: /hide password/i }))
    expect(field).toHaveAttribute('type', 'password')
  })

  it('reports a generic failure for a wrong password and a cooldown when rate limited', async () => {
    mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': [
        { status: 401, body: { error: 'invalid credentials' } },
        { status: 429, body: { error: 'too many failed logins' } },
      ],
    })
    renderApp()
    const user = userEvent.setup()
    const field = await screen.findByLabelText(/^password$/i)
    await user.type(field, 'wrong')
    await user.click(screen.getByRole('button', { name: /sign in/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/password not accepted/i)
    expect(screen.getByRole('alert')).not.toHaveTextContent(/invalid credentials/)
    await user.clear(field)
    await user.type(field, 'wrong again')
    await user.click(screen.getByRole('button', { name: /sign in/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/too many attempts/i)
  })

  it('submits with Enter, shows a busy state, and lands on the inbox', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { csrf_token: 'csrf-new', expires_at: futureSessionExpiry() } },
      ...homeRoutes,
    })
    renderApp()
    const user = userEvent.setup()
    const field = await screen.findByLabelText(/^password$/i)
    await user.type(field, 'correct horse{Enter}')
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    const login = calls.find((call) => call.path === '/admin/api/login')
    expect(login?.body).toEqual({ password: 'correct horse', remember: true })
    expect(login?.init.method).toBe('POST')
    expect(calls.some((call) => call.path === '/admin/api/inbox')).toBe(true)
    await waitFor(() => expect(screen.queryByRole('button', { name: /signing in/i })).not.toBeInTheDocument())
  })

  it('keeps the session bootstrap out of the login flow when already signed in', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes })
    renderApp()
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument()
  })

  it('sends remember: false when the box is unchecked', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { csrf_token: 'csrf-new', expires_at: futureSessionExpiry(), remember: false } },
      ...homeRoutes,
    })
    renderApp()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('checkbox', { name: /remember this device/i }))
    await user.type(screen.getByLabelText(/^password$/i), 'correct horse{Enter}')
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    expect(calls.find((call) => call.path === '/admin/api/login')?.body).toEqual({ password: 'correct horse', remember: false })
  })
})

describe('passkey sign-in', () => {
  const usedHere = 'ledger.passkey-used'
  afterEach(() => localStorage.removeItem(usedHere))

  it('signs in with a passkey in one step', async () => {
    const passkeys = stubPasskeys()
    localStorage.setItem(usedHere, 'yes')
    const { calls } = mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login/passkey/options': { body: { challenge_id: 'ch-1', options: requestOptionsJSON } },
      'POST /admin/api/login/passkey': { body: { csrf_token: 'csrf-pk', expires_at: futureSessionExpiry(), remember: true } },
      ...homeRoutes,
    })
    renderApp()
    const user = userEvent.setup()
    const button = await screen.findByRole('button', { name: /sign in with a passkey/i })
    expect(button).toHaveClass('btn-primary')
    // The primary way in has focus, not the password field (which on a phone would open the keyboard).
    expect(button).toHaveFocus()
    // The password stays available, as the secondary way in.
    expect(screen.getByRole('button', { name: /^sign in$/i })).not.toHaveClass('btn-primary')
    // Remember applies to the passkey too, so it sits above both ways in.
    const remember = screen.getByRole('checkbox', { name: /remember this device/i })
    expect(remember.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    await user.click(button)
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    expect(passkeys.get).toHaveBeenCalledWith({ publicKey: { ...requestOptionsJSON, parsed: 'request' }, signal: expect.any(AbortSignal) })
    expect(calls.find((call) => call.path === '/admin/api/login/passkey')?.body).toEqual({ challenge_id: 'ch-1', credential: passkeys.assertion, remember: true })
    expect(calls.some((call) => call.path === '/admin/api/login')).toBe(false)
  })

  it('leads with the password until this browser has used a passkey', async () => {
    stubPasskeys()
    mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login/passkey/options': { body: { challenge_id: 'ch-1', options: requestOptionsJSON } },
      'POST /admin/api/login/passkey': { body: { csrf_token: 'csrf-pk', expires_at: futureSessionExpiry(), remember: true } },
      ...homeRoutes,
    })
    renderApp()
    const user = userEvent.setup()
    const button = await screen.findByRole('button', { name: /sign in with a passkey/i })
    // Right after the upgrade no passkey exists, so the primary button must be one that works.
    expect(button).not.toHaveClass('btn-primary')
    expect(screen.getByRole('button', { name: /^sign in$/i })).toHaveClass('btn-primary')
    expect(screen.getByLabelText(/^password$/i)).toHaveFocus()
    await user.click(button)
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    expect(localStorage.getItem(usedHere)).toBe('yes')
  })

  it('says so quietly when the prompt is closed, and explains when no passkey exists yet', async () => {
    const passkeys = stubPasskeys()
    localStorage.setItem(usedHere, 'yes')
    passkeys.get.mockRejectedValueOnce(new DOMException('closed', 'NotAllowedError'))
    const { calls } = mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login/passkey/options': [
        { body: { challenge_id: 'ch-1', options: requestOptionsJSON } },
        { status: 409, body: { error: 'no passkeys' } },
      ],
    })
    renderApp()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /sign in with a passkey/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/prompt closed/i)
    expect(calls.some((call) => call.path === '/admin/api/login/passkey')).toBe(false)
    await user.click(screen.getByRole('button', { name: /sign in with a passkey/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/no passkey is set up yet/i)
    expect(screen.getByRole('alert')).not.toHaveTextContent('no passkeys')
    // With no passkey on the server (after reset-passkeys, say) the password leads again.
    expect(screen.getByRole('button', { name: /^sign in$/i })).toHaveClass('btn-primary')
    expect(screen.getByRole('button', { name: /sign in with a passkey/i })).not.toHaveClass('btn-primary')
    expect(localStorage.getItem(usedHere)).toBeNull()
    // The message says to use the password, so focus goes there and not back to the passkey button.
    await waitFor(() => expect(screen.getByLabelText(/^password$/i)).toHaveFocus())
  })

  it('puts focus back on the passkey button after a closed prompt, so Enter retries', async () => {
    const passkeys = stubPasskeys()
    // The prompt stays open a moment, as a real one does, so the page renders its waiting state first.
    passkeys.get.mockImplementation(() => new Promise((_, reject) => setTimeout(() => reject(new DOMException('closed', 'NotAllowedError')), 20)))
    mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { passkey_required: true, pending: 'pending-1', options: requestOptionsJSON } },
    })
    renderApp()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(/^password$/i), 'correct horse{Enter}')
    expect(await screen.findByRole('alert')).toHaveTextContent(/prompt closed/i)
    await waitFor(() => expect(screen.getByRole('button', { name: /use passkey/i })).toHaveFocus())
    // The error sits under the control that failed, as on the main card.
    expect(screen.getByRole('button', { name: /use passkey/i }).compareDocumentPosition(screen.getByRole('alert')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(passkeys.get).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByRole('button', { name: /use passkey/i })).toHaveFocus())
  })

  it('finishes a password sign-in with a passkey, retrying after a closed prompt', async () => {
    const passkeys = stubPasskeys()
    passkeys.get.mockRejectedValueOnce(new DOMException('closed', 'NotAllowedError'))
    const { calls } = mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { passkey_required: true, pending: 'pending-1', options: requestOptionsJSON } },
      'POST /admin/api/login/passkey': { body: { csrf_token: 'csrf-pk', expires_at: futureSessionExpiry(), remember: true } },
      ...homeRoutes,
    })
    renderApp()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(/^password$/i), 'correct horse{Enter}')
    // The password alone did not sign in.
    expect(await screen.findByRole('heading', { name: /confirm with your passkey/i })).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent(/prompt closed/i)
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument()
    expect(calls.some((call) => call.path === '/admin/api/login/passkey')).toBe(false)
    await user.click(screen.getByRole('button', { name: /use passkey/i }))
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    expect(passkeys.get).toHaveBeenCalledTimes(2)
    expect(calls.find((call) => call.path === '/admin/api/login')?.body).toEqual({ password: 'correct horse', remember: true })
    expect(calls.find((call) => call.path === '/admin/api/login/passkey')?.body).toEqual({ pending: 'pending-1', credential: passkeys.assertion, remember: true })
  })

  it('goes back to the password when asked, and when the server refuses the passkey', async () => {
    const passkeys = stubPasskeys()
    passkeys.get.mockRejectedValueOnce(new DOMException('closed', 'NotAllowedError'))
    mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { passkey_required: true, pending: 'pending-1', options: requestOptionsJSON } },
      'POST /admin/api/login/passkey': { status: 401, body: { error: 'passkey not accepted' } },
    })
    renderApp()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(/^password$/i), 'correct horse{Enter}')
    await screen.findByRole('heading', { name: /confirm with your passkey/i })
    await user.click(await screen.findByRole('button', { name: /back to password/i }))
    expect(screen.getByRole('heading', { name: /welcome back/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/^password$/i)).toHaveValue('')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await user.type(screen.getByLabelText(/^password$/i), 'correct horse{Enter}')
    // The pending sign-in is single use: a refused passkey starts over at the password.
    expect(await screen.findByRole('alert')).toHaveTextContent('Passkey not accepted.')
    expect(screen.getByRole('heading', { name: /welcome back/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/^password$/i)).toBeEnabled()
  })

  // A prompt that never answers (a dismissed popup that does not reject) is closed from the page.
  function hangUntilAborted(passkeys: ReturnType<typeof stubPasskeys>) {
    passkeys.get.mockImplementation((options) => new Promise((_, reject) => {
      options.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    }))
  }

  it('cancels a one-step passkey prompt that never answers', async () => {
    const passkeys = stubPasskeys()
    localStorage.setItem(usedHere, 'yes')
    hangUntilAborted(passkeys)
    const { calls } = mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login/passkey/options': { body: { challenge_id: 'ch-1', options: requestOptionsJSON } },
    })
    renderApp()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /sign in with a passkey/i }))
    expect(await screen.findByText(/waiting for your passkey/i)).toBeInTheDocument()
    await waitFor(() => expect(passkeys.get).toHaveBeenCalledTimes(1))
    await user.click(screen.getByRole('button', { name: /^cancel$/i }))
    expect(passkeys.get.mock.calls[0]![0].signal?.aborted).toBe(true)
    const button = screen.getByRole('button', { name: /sign in with a passkey/i })
    expect(button).toBeEnabled()
    await waitFor(() => expect(button).toHaveFocus())
    expect(screen.queryByRole('button', { name: /^cancel$/i })).not.toBeInTheDocument()
    // Cancelling is not a failure.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(calls.some((call) => call.path === '/admin/api/login/passkey')).toBe(false)
  })

  it('hides the session-ended notice while a passkey prompt is open', async () => {
    const passkeys = stubPasskeys()
    hangUntilAborted(passkeys)
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/inbox': { status: 401, body: { error: 'unauthenticated' } },
      'GET /admin/api/table/projects': { status: 401, body: { error: 'unauthenticated' } },
      'POST /admin/api/login/passkey/options': { body: { challenge_id: 'ch-1', options: requestOptionsJSON } },
    })
    renderApp()
    const user = userEvent.setup()
    expect(await screen.findByText('Your session ended. Sign in again.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /sign in with a passkey/i }))
    expect(await screen.findByRole('button', { name: /^cancel$/i })).toBeInTheDocument()
    expect(screen.queryByText('Your session ended. Sign in again.')).not.toBeInTheDocument()
  })

  it('goes back to the password while the passkey step is still waiting', async () => {
    const passkeys = stubPasskeys()
    localStorage.setItem(usedHere, 'yes')
    hangUntilAborted(passkeys)
    mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { passkey_required: true, pending: 'pending-1', options: requestOptionsJSON } },
    })
    renderApp()
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(/^password$/i), 'correct horse{Enter}')
    await screen.findByRole('heading', { name: /confirm with your passkey/i })
    expect(screen.getByRole('button', { name: /waiting for your passkey/i })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: /back to password/i }))
    expect(passkeys.get.mock.calls[0]![0].signal?.aborted).toBe(true)
    expect(screen.getByRole('heading', { name: /welcome back/i })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    // Even where the passkey leads, the owner chose the password: its field takes focus.
    await waitFor(() => expect(screen.getByLabelText(/^password$/i)).toHaveFocus())
    expect(screen.getByLabelText(/^password$/i)).toBeEnabled()
  })
})
