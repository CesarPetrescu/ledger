import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { authenticatedSession, mockApi, overview, renderApp } from './helpers'

describe('overview', () => {
  it('shows real counts, tier-grouped projects, and recent activity as text', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/overview': { body: overview } })
    renderApp()
    expect(await screen.findByText(/loading overview/i)).toBeInTheDocument()
    await screen.findByRole('heading', { name: /^overview$/i })
    const counts = screen.getByRole('list', { name: /counts/i })
    expect(within(counts).getByText('Projects').nextElementSibling).toHaveTextContent('2')
    expect(within(counts).getByText('Entries').nextElementSibling).toHaveTextContent('2')
    expect(within(counts).getByText('OAuth clients').nextElementSibling).toHaveTextContent('1')
    expect(within(counts).getByText('Active tokens').nextElementSibling).toHaveTextContent('3')
    expect(within(counts).getByText('Admin sessions').nextElementSibling).toHaveTextContent('1')
    const focus = screen.getByRole('region', { name: /^focus/i })
    expect(within(focus).getByRole('link', { name: /atlas/i })).toHaveAttribute('href', '/admin/projects/atlas')
    const park = screen.getByRole('region', { name: /^park/i })
    expect(within(park).getByRole('link', { name: /beacon/i })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: /^maintain/i })).not.toBeInTheDocument()
    const activity = screen.getByRole('region', { name: /recent activity/i })
    expect(within(activity).getByText('Use PostgreSQL <b>everywhere</b>.')).toBeInTheDocument()
    expect(activity.querySelector('b')).toBeNull()
    expect(within(activity).getAllByText(/decision|note/)).not.toHaveLength(0)
  })

  it('renders an empty state that leads to project creation', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/overview': { body: { counts: { projects: 0, entries: 0, oauth_clients: 0, active_access_tokens: 0, active_admin_sessions: 1 }, projects: [], recent_entries: [] } },
    })
    renderApp()
    expect(await screen.findByText(/no projects yet/i)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /create a project/i })).toHaveAttribute('href', '/admin/projects/new')
    expect(screen.getByText(/no memory activity yet/i)).toBeInTheDocument()
  })

  it('shows an error state and recovers on retry', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/overview': [{ status: 500, body: { error: 'internal server error' } }, { body: overview }],
    })
    renderApp()
    expect(await screen.findByRole('alert')).toHaveTextContent(/couldn.t load the overview/i)
    expect(screen.getByRole('alert')).not.toHaveTextContent(/internal server error/)
    await userEvent.setup().click(screen.getByRole('button', { name: /retry/i }))
    expect(await screen.findByRole('region', { name: /^focus/i })).toBeInTheDocument()
  })
})
