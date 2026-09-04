import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { anonymousSession, authenticatedSession, clients, mockApi, overview, renderApp } from './helpers'

describe('authenticated shell and routing', () => {
  it('renders navigation, routes by path, and signs out with CSRF', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/overview': { body: overview },
      'GET /admin/api/oauth/clients': { body: { clients } },
      'POST /admin/api/logout': { status: 204 },
    })
    renderApp('/admin/clients')
    expect(await screen.findByRole('heading', { name: /oauth clients/i })).toBeInTheDocument()
    const nav = screen.getByRole('navigation', { name: /primary/i })
    for (const label of ['Overview', 'Projects', 'Search', 'Clients']) {
      expect(within(nav).getByRole('link', { name: label })).toBeInTheDocument()
    }
    expect(within(nav).getByRole('link', { name: 'Clients' })).toHaveAttribute('aria-current', 'page')
    const user = userEvent.setup()
    await user.click(within(nav).getByRole('link', { name: 'Overview' }))
    expect(await screen.findByRole('heading', { name: /^overview$/i })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/admin/')
    await user.click(screen.getByRole('button', { name: /sign out/i }))
    expect(await screen.findByRole('heading', { name: /operator sign-in/i })).toBeInTheDocument()
    expect(screen.getByText(/signed out/i)).toBeInTheDocument()
    const logout = calls.find((call) => call.path === '/admin/api/logout')
    expect(new Headers(logout?.init.headers).get('X-CSRF-Token')).toBe('csrf-123')
  })

  it('drops to the sign-in panel with an expiry notice when the API answers 401 mid-session', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/overview': { status: 401, body: { error: 'unauthenticated' } },
    })
    renderApp()
    expect(await screen.findByRole('heading', { name: /operator sign-in/i })).toBeInTheDocument()
    expect(screen.getByText(/session expired/i)).toBeInTheDocument()
  })

  it('shows the sign-in panel for deep links without a session and returns there after sign-in', async () => {
    mockApi({
      'GET /admin/api/session': anonymousSession,
      'POST /admin/api/login': { body: { csrf_token: 'csrf-new', expires_at: '2026-09-04T20:00:00Z' } },
      'GET /admin/api/oauth/clients': { body: { clients } },
    })
    renderApp('/admin/clients')
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(/^password$/i), 'correct horse{Enter}')
    expect(await screen.findByRole('heading', { name: /oauth clients/i })).toBeInTheDocument()
  })

  it('opens search from the keyboard shortcut', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/overview': { body: overview },
      'GET /admin/api/projects': { body: { projects: overview.projects } },
    })
    renderApp()
    await screen.findByRole('heading', { name: /^overview$/i })
    const user = userEvent.setup()
    await user.keyboard('{Control>}k{/Control}')
    expect(await screen.findByRole('heading', { name: /^search$/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/search memory/i)).toHaveFocus()
  })
})
