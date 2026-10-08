import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { authenticatedSession, clients, mockApi, overview, renderApp } from './helpers'

describe('oauth clients', () => {
  it('creates an API key, shows its secret once, and revokes it', async () => {
    const key = { id: 7, name: 'Adastrion Core', prefix: 'ledger_AbCdEfG', scopes: ['research:dispatch'], created_at: '2026-10-06T08:00:00Z' }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/oauth/clients': { body: { clients: [] } },
      'GET /admin/api/agents': { body: { agents: [] } },
      'GET /admin/api/api-keys': [{ body: { keys: [] } }, { body: { keys: [key] } }, { body: { keys: [{ ...key, revoked_at: '2026-10-06T09:00:00Z' }] } }],
      'POST /admin/api/api-keys': { status: 201, body: { key, secret: 'ledger_AbCdEfGsecret-value' } },
      'DELETE /admin/api/api-keys/7': { body: { ...key, revoked_at: '2026-10-06T09:00:00Z' } },
    })
    renderApp('/admin/agents')
    const section = await screen.findByRole('region', { name: 'API keys' })
    expect(await within(section).findByText('No API keys yet.')).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(within(section).getByLabelText('Key name'), 'Adastrion Core')
    await user.click(within(section).getByRole('button', { name: 'Create key' }))
    const reveal = await within(section).findByRole('status')
    expect(reveal).toHaveTextContent('ledger_AbCdEfGsecret-value')
    expect(reveal).toHaveTextContent(/will not show it again/i)
    expect(calls.find((call) => call.method === 'POST' && call.path === '/admin/api/api-keys')?.body).toEqual({ name: 'Adastrion Core' })
    const table = await within(section).findByRole('table', { name: 'API keys' })
    expect(table).toHaveTextContent('ledger_AbCdEfG…')
    expect(table).toHaveTextContent('Dispatch research')
    expect(table.textContent).not.toContain('secret-value')
    await user.click(within(section).getByRole('button', { name: 'Done' }))
    expect(within(section).queryByText('ledger_AbCdEfGsecret-value')).not.toBeInTheDocument()
    await user.click(within(table).getByRole('button', { name: 'Revoke' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(calls.some((call) => call.method === 'DELETE' && call.path === '/admin/api/api-keys/7')).toBe(true))
    expect(await within(section).findByText(/^Revoked/)).toBeInTheDocument()
  })

  it('saves the GitHub sync token without ever showing it, then turns sync off', async () => {
    const token = 'github_pat_SECRETVALUE0123456789wxyz'
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/oauth/clients': { body: { clients: [] } },
      'GET /admin/api/agents': { body: { agents: [] } },
      'GET /admin/api/api-keys': { body: { keys: [] } },
      'GET /admin/api/github-sync': { body: { configured: false } },
      'PUT /admin/api/github-sync': { body: { configured: true, hint: '…wxyz', login: 'CesarPetrescu', saved_at: '2026-10-08T10:00:00Z' } },
      'DELETE /admin/api/github-sync': { status: 204 },
    })
    renderApp('/admin/agents')
    const section = await screen.findByRole('region', { name: 'GitHub sync' })
    const user = userEvent.setup()
    const field = await within(section).findByLabelText('Token')
    expect(field).toHaveAttribute('type', 'password')
    await user.type(field, token)
    await user.click(within(section).getByRole('button', { name: 'Save token' }))
    expect(await within(section).findByText('CesarPetrescu')).toBeInTheDocument()
    expect(section).toHaveTextContent('…wxyz')
    expect(section.textContent).not.toContain('SECRETVALUE')
    expect(within(section).getByLabelText('Replace token')).toHaveValue('')
    expect(calls.find((call) => call.method === 'PUT' && call.path === '/admin/api/github-sync')?.body).toEqual({ token })
    await user.click(within(section).getByRole('button', { name: 'Turn off' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Turn off' }))
    await waitFor(() => expect(calls.some((call) => call.method === 'DELETE' && call.path === '/admin/api/github-sync')).toBe(true))
    expect(await within(section).findByLabelText('Token')).toBeInTheDocument()
  })

  it('lists safe metadata only', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/oauth/clients': { body: { clients } }, 'GET /admin/api/overview': { body: overview } })
    renderApp('/admin/clients')
    // The counters the old overview page showed live here now.
    const counts = await screen.findByRole('list', { name: 'Counts' })
    expect(counts).toHaveTextContent('Active access tokens3')
    const table = await screen.findByRole('table', { name: /oauth clients/i })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('Agent One')
    expect(rows[0]).toHaveTextContent('Dynamic registration')
    expect(rows[0]).toHaveTextContent('http://127.0.0.1:4567/callback')
    expect(rows[0]).toHaveTextContent('3')
    expect(rows[1]).toHaveTextContent('Client ID metadata')
    expect(rows[1]).toHaveTextContent('https://app.example/client.json')
    expect(rows[1]!.querySelector('[data-label="Access tokens"]')).toHaveTextContent('0')
    expect(rows[1]!.querySelector('[data-label="Refresh tokens"]')).toHaveTextContent('1')
    expect(table.textContent).not.toMatch(/hash|secret|refresh_token/i)
    const firstRowCells = within(rows[0]!).getAllByRole('cell')
    expect(firstRowCells.map((cell) => cell.getAttribute('data-label'))).toEqual(['Name', 'Type', 'Client ID', 'Redirect URIs', 'Created', 'Last used', 'Access tokens', 'Refresh tokens', 'Actions'])
  })

  it('requires explicit confirmation before revoking and reports the result', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/oauth/clients': [{ body: { clients } }, { body: { clients: [{ ...clients[0]!, active_access_tokens: 0, active_refresh_tokens: 0 }, clients[1]!] } }],
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

  it('says so when the counts fail to load, with a retry', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/oauth/clients': { body: { clients } }, 'GET /admin/api/overview': { status: 500, body: { error: 'hidden' } } })
    renderApp('/admin/clients')
    expect(await screen.findByText("Couldn't load the counts.")).toBeInTheDocument()
  })

  it('shows an empty state when nothing is registered', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/oauth/clients': { body: { clients: [] } }, 'GET /admin/api/agents': { body: { agents: [] } } })
    renderApp('/admin/clients')
    expect(await screen.findByText(/no apps have connected yet/i)).toBeInTheDocument()
    // With no agent yet, the page explains how to connect one, with this server's address.
    expect(screen.getByText(/no agent has written to ledger yet/i)).toBeInTheDocument()
    const guide = screen.getByRole('region', { name: 'Connect an agent' })
    expect(guide).toHaveTextContent(`${window.location.origin}/mcp`)
    expect(guide).toHaveTextContent(`ledger connect codex --server ${window.location.origin}`)
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
