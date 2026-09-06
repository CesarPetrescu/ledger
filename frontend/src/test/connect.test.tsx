import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { anonymousSession, futureSessionExpiry, mockApi, renderApp } from './helpers'

it('requires login, code review, and an explicit decision before connecting', async () => {
  const { calls } = mockApi({
    'GET /admin/api/session': anonymousSession,
    'POST /admin/api/login': { body: { csrf_token: 'approval-csrf', expires_at: futureSessionExpiry() } },
    'POST /admin/api/oauth/device': [
      { body: { user_code: 'ABCD2345', client_name: 'Atlas machine', scope: 'ledger:read ledger:write', created_at: '2026-09-01T10:00:00Z', expires_at: futureSessionExpiry() } },
      { status: 204 },
    ],
  })
  renderApp('/admin/connect')
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText(/^password$/i), 'correct horse{Enter}')
  await user.type(await screen.findByLabelText(/connection code/i), 'ABCD-2345')
  expect(calls.filter(call => call.path.endsWith('/oauth/device'))).toHaveLength(0)
  await user.click(screen.getByRole('button', { name: 'Review connection' }))
  expect(await screen.findByText('Read project memory')).toBeInTheDocument()
  expect(screen.queryByText('Read selected calendars')).not.toBeInTheDocument()
  expect(calls.filter(call => call.path.endsWith('/oauth/device'))).toHaveLength(1)
  await user.click(screen.getByRole('button', { name: 'Approve machine' }))
  expect(await screen.findByText(/Machine approved/)).toBeInTheDocument()
  const decision = calls.filter(call => call.path.endsWith('/oauth/device'))[1]!
  expect(decision.body).toEqual({ user_code: 'ABCD2345', action: 'approve' })
  expect(new Headers(decision.init.headers).get('X-CSRF-Token')).toBe('approval-csrf')
})
