import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { anonymousSession, authenticatedSession, futureSessionExpiry, mockApi, overview, renderApp } from './helpers'

const query = 'client_id=desk-client&state=state%26private&code_challenge=challenge'

it('keeps the OAuth request through owner login and requires an explicit decision', async () => {
  const { calls } = mockApi({
    'GET /admin/api/session': anonymousSession,
    'POST /admin/api/login': { body: { csrf_token: 'owner-csrf', expires_at: futureSessionExpiry() } },
    'GET /admin/api/oauth/authorize': { body: { client_name: 'Desk app', scopes: ['ledger:read', 'calendar:write'] } },
    'POST /admin/api/oauth/authorize': { body: { redirect_url: 'https://client.example/callback?code=approved&state=state%26private' } },
  })
  renderApp(`/admin/authorize?${query}`)
  const user = userEvent.setup()
  expect(calls.some(call => call.path === '/admin/api/oauth/authorize')).toBe(false)
  await user.type(await screen.findByLabelText(/^password$/i), 'correct horse{Enter}')
  expect(await screen.findByRole('heading', { name: 'Allow Desk app to use Ledger?' })).toBeInTheDocument()
  expect(screen.getByText('Read project memory')).toBeInTheDocument()
  expect(screen.getByText(/Create, update, and delete events/)).toBeInTheDocument()
  expect(screen.queryByLabelText('Approval password')).not.toBeInTheDocument()
  expect(calls.filter(call => call.method === 'POST' && call.path === '/admin/api/oauth/authorize')).toHaveLength(0)
  await user.click(screen.getByRole('button', { name: 'Allow access' }))
  expect(await screen.findByRole('link', { name: 'Return to the client' })).toHaveAttribute('href', 'https://client.example/callback?code=approved&state=state%26private')
  const decision = calls.find(call => call.method === 'POST' && call.path === '/admin/api/oauth/authorize')!
  expect(decision.body).toEqual({ action: 'approve' })
  expect(decision.url.searchParams.get('state')).toBe('state&private')
  expect(decision.url.searchParams.get('code_challenge')).toBe('challenge')
  expect(new Headers(decision.init.headers).get('X-CSRF-Token')).toBe('owner-csrf')
})

it('allows denial and never offers approval for an invalid request', async () => {
  const { calls } = mockApi({
    'GET /admin/api/session': authenticatedSession,
    'GET /admin/api/oauth/authorize': [
      { status: 400, body: { error: 'Invalid request.' } },
      { body: { client_name: 'Desk app', scopes: ['ledger:read'] } },
    ],
    'POST /admin/api/oauth/authorize': { body: { redirect_url: 'https://client.example/callback?error=access_denied' } },
  })
  renderApp(`/admin/authorize?${query}`)
  expect(await screen.findByText('Invalid request.')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Allow access' })).not.toBeInTheDocument()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /retry/i }))
  await user.click(await screen.findByRole('button', { name: 'Deny' }))
  expect(await screen.findByRole('link', { name: 'Return to the client' })).toHaveAttribute('href', 'https://client.example/callback?error=access_denied')
  expect(calls.find(call => call.method === 'POST')?.body).toEqual({ action: 'deny' })
})

it('resets the approval password from Agents using the owner password and clears the fields', async () => {
  const { calls } = mockApi({
    'GET /admin/api/session': authenticatedSession,
    'GET /admin/api/agents': { body: { agents: [] } },
    'GET /admin/api/oauth/clients': { body: { clients: [] } },
    'GET /admin/api/overview': { body: overview },
    'GET /admin/api/api-keys': { body: { keys: [] } },
    'GET /admin/api/github-sync': { body: { configured: false } },
    'PUT /admin/api/oauth/password': [
      { status: 403, body: { error: 'Password not accepted.' } },
      { status: 204 },
    ],
  })
  renderApp('/admin/agents')
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText('Owner password'), 'wrong')
  await user.type(screen.getByLabelText('New approval password'), 'new approval secret')
  await user.type(screen.getByLabelText('Confirm new approval password'), 'different password')
  await user.click(screen.getByRole('button', { name: 'Change approval password' }))
  expect(screen.getByRole('alert')).toHaveTextContent('The new passwords do not match.')
  expect(calls.some(call => call.method === 'PUT')).toBe(false)
  await user.clear(screen.getByLabelText('Confirm new approval password'))
  await user.type(screen.getByLabelText('Confirm new approval password'), 'new approval secret')
  await user.click(screen.getByRole('button', { name: 'Change approval password' }))
  expect(await screen.findByText('Password not accepted.')).toBeInTheDocument()
  await user.clear(screen.getByLabelText('Owner password'))
  await user.type(screen.getByLabelText('Owner password'), 'correct horse')
  await user.click(screen.getByRole('button', { name: 'Change approval password' }))
  expect(await screen.findByText('Approval password changed.')).toBeInTheDocument()
  for (const label of ['Owner password', 'New approval password', 'Confirm new approval password']) expect(screen.getByLabelText(label)).toHaveValue('')
  const change = calls.filter(call => call.method === 'PUT')[1]!
  expect(change.body).toEqual({ current_password: 'correct horse', new_password: 'new approval secret' })
  expect(new Headers(change.init.headers).get('X-CSRF-Token')).toBe('csrf-123')
})
