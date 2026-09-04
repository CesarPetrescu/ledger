import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { authenticatedSession, clients, mockApi, renderApp } from './helpers'

describe('oauth clients', () => {
  it('lists safe metadata only', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/oauth/clients': { body: { clients } } })
    renderApp('/admin/clients')
    const table = await screen.findByRole('table', { name: /oauth clients/i })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('Agent One')
    expect(rows[0]).toHaveTextContent('Dynamic registration')
    expect(rows[0]).toHaveTextContent('http://127.0.0.1:4567/callback')
    expect(rows[0]).toHaveTextContent('3')
    expect(rows[1]).toHaveTextContent('Client ID metadata')
    expect(rows[1]).toHaveTextContent('https://app.example/client.json')
    expect(table.textContent).not.toMatch(/hash|secret|refresh_token/i)
    const firstRowCells = within(rows[0]!).getAllByRole('cell')
    expect(firstRowCells.map((cell) => cell.getAttribute('data-label'))).toEqual(['Name', 'Type', 'Client ID', 'Redirect URIs', 'Created', 'Last used', 'Active tokens', 'Actions'])
  })

  it('requires explicit confirmation before revoking and reports the result', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/oauth/clients': [{ body: { clients } }, { body: { clients: [{ ...clients[0]!, active_access_tokens: 0 }, clients[1]!] } }],
      'POST /admin/api/oauth/revoke': { body: { revoked: 3 } },
    })
    renderApp('/admin/clients')
    await screen.findByRole('table', { name: /oauth clients/i })
    const user = userEvent.setup()
    await user.click(screen.getAllByRole('button', { name: /revoke tokens/i })[0]!)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(/revoke tokens for agent one/i)
    await user.click(within(dialog).getByRole('button', { name: /cancel/i }))
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await user.click(screen.getAllByRole('button', { name: /revoke tokens/i })[0]!)
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /^revoke$/i }))
    expect(await screen.findByRole('status')).toHaveTextContent(/revoked 3 tokens/i)
    const post = calls.find((call) => call.method === 'POST')
    expect(post?.path).toBe('/admin/api/oauth/revoke')
    expect(post?.body).toEqual({ client_id: 'dcr-client-abc' })
    expect(new Headers(post?.init.headers).get('X-CSRF-Token')).toBe('csrf-123')
    await waitFor(() => expect(calls.filter((call) => call.path === '/admin/api/oauth/clients')).toHaveLength(2))
  })

  it('shows an empty state when nothing is registered', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/oauth/clients': { body: { clients: [] } } })
    renderApp('/admin/clients')
    expect(await screen.findByText(/no oauth clients registered/i)).toBeInTheDocument()
  })

  it('navigates bounded client pages', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/oauth/clients': [{ body: { clients: [clients[0]], next_offset: 50 } }, { body: { clients: [clients[1]] } }],
    })
    renderApp('/admin/clients')
    expect(await screen.findByText('Agent One')).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /next page/i }))
    expect(await screen.findByText('Desk app')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /previous page/i })).toBeEnabled()
    expect(screen.queryByRole('button', { name: /next page/i })).not.toBeInTheDocument()
    expect(calls.some((call) => call.url.search === '?limit=50&offset=50')).toBe(true)
  })
})
