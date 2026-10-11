import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { anonymousSession, authenticatedSession, futureSessionExpiry, homeRoutes, mockApi, renderApp, requestOptionsJSON, stubPasskeys } from './helpers'

describe('login', () => {
  it('shows a compact sign-in, without a passkey button where the browser has no passkeys', async () => {
    mockApi({ 'GET /admin/api/session': anonymousSession })
    renderApp()
    expect(await screen.findByRole('heading', { name: /welcome back/i })).toBeInTheDocument()
    expect(screen.getByText(/operator sign-in/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^password$/i)).toHaveAttribute('type', 'password')
    expect(screen.queryByLabelText(/username|email/i)).not.toBeInTheDocument()
    expect(document.querySelectorAll('input')).toHaveLength(2)
    // A personal console: remembered by default, with a hint for shared computers.
    expect(screen.getByRole('checkbox', { name: /remember this device for 30 days/i })).toBeChecked()
    expect(screen.getByText(/uncheck on a shared computer/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /passkey/i })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^sign in$/i })).toHaveClass('btn-primary')
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
  it('signs in with a passkey in one step', async () => {
    const passkeys = stubPasskeys()
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
    // The password stays available, as the secondary way in.
    expect(screen.getByRole('button', { name: /^sign in$/i })).not.toHaveClass('btn-primary')
    await user.click(button)
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    expect(passkeys.get).toHaveBeenCalledWith({ publicKey: { ...requestOptionsJSON, parsed: 'request' } })
    expect(calls.find((call) => call.path === '/admin/api/login/passkey')?.body).toEqual({ challenge_id: 'ch-1', credential: passkeys.assertion, remember: true })
    expect(calls.some((call) => call.path === '/admin/api/login')).toBe(false)
  })

  it('says so quietly when the prompt is closed, and explains when no passkey exists yet', async () => {
    const passkeys = stubPasskeys()
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
})
