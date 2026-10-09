import { act, screen, within } from '@testing-library/react'
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
    expect(items).toEqual(['Help', 'Theme: System (switch to Light)', 'Access', 'Sign out'])

    await user.click(screen.getByRole('menuitem', { name: 'Theme: System (switch to Light)' }))
    expect(document.documentElement.dataset.theme).toBe('light')
    await user.click(menu)
    expect(screen.getByRole('menuitem', { name: 'Theme: Light (switch to Dark)' })).toBeInTheDocument()

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

  it('shows the live status once, in the top bar, and keeps the session note in the sidebar', async () => {
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket)
    mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes })
    renderApp()
    await screen.findByRole('heading', { name: /^inbox$/i })
    act(() => FakeWebSocket.last?.onopen?.({} as Event))

    const live = screen.getAllByText(/^live/i)
    expect(live).toHaveLength(1)
    expect(live[0]?.closest('header')).toHaveClass('topbar')
    expect(screen.queryByText(/live updates on/i)).not.toBeInTheDocument()
    expect(screen.getByRole('complementary')).toHaveTextContent(/session ends/i)
  })
})
