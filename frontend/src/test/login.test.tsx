import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { anonymousSession, authenticatedSession, futureSessionExpiry, mockApi, overview, renderApp } from './helpers'

describe('login', () => {
  it('shows a compact password-only sign-in when there is no session', async () => {
    mockApi({ 'GET /admin/api/session': anonymousSession })
    renderApp()
    expect(await screen.findByRole('heading', { name: /operator sign-in/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/^password$/i)).toHaveAttribute('type', 'password')
    expect(screen.queryByLabelText(/username|email/i)).not.toBeInTheDocument()
    expect(document.querySelectorAll('input')).toHaveLength(1)
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

  it('submits with Enter, shows a busy state, and lands on the overview', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { csrf_token: 'csrf-new', expires_at: futureSessionExpiry() } },
      'GET /admin/api/overview': { body: overview },
    })
    renderApp()
    const user = userEvent.setup()
    const field = await screen.findByLabelText(/^password$/i)
    await user.type(field, 'correct horse{Enter}')
    expect(await screen.findByRole('heading', { name: /^overview$/i })).toBeInTheDocument()
    const login = calls.find((call) => call.path === '/admin/api/login')
    expect(login?.body).toEqual({ password: 'correct horse' })
    expect(login?.init.method).toBe('POST')
    const overviewCall = calls.find((call) => call.path === '/admin/api/overview')
    expect(overviewCall).toBeDefined()
    await waitFor(() => expect(screen.queryByRole('button', { name: /signing in/i })).not.toBeInTheDocument())
  })

  it('keeps the session bootstrap out of the login flow when already signed in', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/overview': { body: overview } })
    renderApp()
    expect(await screen.findByRole('heading', { name: /^overview$/i })).toBeInTheDocument()
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument()
  })
})
