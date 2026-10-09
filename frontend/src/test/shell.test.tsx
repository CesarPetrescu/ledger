import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { navigate } from '../router'
import { applyTheme } from '../theme'
import { authenticatedSession, homeRoutes, mockApi, renderApp } from './helpers'

class FakeWebSocket {
  static last: FakeWebSocket | null = null
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  constructor(readonly url: string) { FakeWebSocket.last = this }
  close() { this.onclose?.({} as CloseEvent) }
}

describe('shell', () => {
  afterEach(() => applyTheme('system'))

  it('lists Access in the sidebar and marks it current on /access and the old /clients address', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes })
    renderApp()
    const nav = await screen.findByRole('navigation', { name: /primary/i })
    const access = within(nav).getByRole('link', { name: 'Access' })
    expect(access).toHaveAttribute('href', '/admin/access')
    expect(access).not.toHaveAttribute('aria-current')

    await userEvent.setup().click(access)
    expect(window.location.pathname).toBe('/admin/access')
    expect(within(nav).getByRole('link', { name: 'Access' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Agents' })).not.toHaveAttribute('aria-current')

    act(() => navigate('/clients'))
    expect(within(nav).getByRole('link', { name: 'Access' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Agents' })).not.toHaveAttribute('aria-current')
  })

  it('keeps Help, theme, Access, and sign-out in one account menu for phones', async () => {
    window.localStorage.removeItem('ledger-theme')
    const { calls } = mockApi({ 'GET /admin/api/session': authenticatedSession, 'POST /admin/api/logout': { status: 204 }, ...homeRoutes })
    renderApp()
    await screen.findByRole('heading', { name: /^inbox$/i })
    const user = userEvent.setup()
    const menu = screen.getByRole('button', { name: 'Account menu' })

    await user.click(menu)
    const items = within(screen.getByRole('menu', { name: 'Account menu' })).getAllByRole('menuitem').map((item) => item.textContent)
    expect(items).toEqual(['Help', 'Light theme', 'Access', 'Sign out'])

    await user.click(screen.getByRole('menuitem', { name: 'Light theme' }))
    expect(document.documentElement.dataset.theme).toBe('light')
    await user.click(menu)
    expect(screen.getByRole('menuitem', { name: 'Dark theme' })).toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Dark theme' }))
    await user.click(menu)
    expect(screen.getByRole('menuitem', { name: 'Match system theme' })).toBeInTheDocument()

    await user.click(screen.getByRole('menuitem', { name: 'Access' }))
    expect(window.location.pathname).toBe('/admin/access')

    await user.click(menu)
    await user.click(screen.getByRole('menuitem', { name: 'Help' }))
    expect(window.location.pathname).toBe('/admin/help')

    await user.click(menu)
    await user.click(screen.getByRole('menuitem', { name: 'Sign out' }))
    expect(await screen.findByRole('heading', { name: /welcome back/i })).toBeInTheDocument()
    expect(calls.some((call) => call.method === 'POST' && call.path === '/admin/api/logout')).toBe(true)
  })

  it('drops the top-bar search trigger on the Search page, which has its own field', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/projects': { body: { projects: [] } }, ...homeRoutes })
    renderApp()
    await screen.findByRole('heading', { name: /^inbox$/i })
    expect(document.querySelector('.topbar .search-trigger')).not.toBeNull()
    act(() => navigate('/search'))
    expect(await screen.findByRole('heading', { level: 1, name: 'Search' })).toBeInTheDocument()
    expect(document.querySelector('.topbar .search-trigger')).toBeNull()
  })

  it('shows the live status once, in the top bar, and keeps the session note in the sidebar', async () => {
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket)
    FakeWebSocket.last = null
    mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes })
    renderApp()
    await screen.findByRole('heading', { name: /^inbox$/i })
    // The heading can render before the Shell's effects open the socket.
    await waitFor(() => expect(FakeWebSocket.last).not.toBeNull())
    act(() => FakeWebSocket.last?.onopen?.({} as Event))

    const live = screen.getAllByText(/^live/i)
    expect(live).toHaveLength(1)
    expect(live[0]?.closest('header')).toHaveClass('topbar')
    expect(screen.queryByText(/live updates on/i)).not.toBeInTheDocument()
    expect(screen.getByRole('complementary')).toHaveTextContent(/session ends/i)
  })
})
