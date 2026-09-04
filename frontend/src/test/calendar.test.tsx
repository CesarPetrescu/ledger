import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { CalendarEvent, CalendarSource } from '../api'
import { authenticatedSession, mockApi, renderApp } from './helpers'

const calendars: CalendarSource[] = [
  { id: 'work', name: 'Work', description: 'Shared schedule', selected: true },
  { id: 'personal', name: 'Personal', selected: false },
]

const planning: CalendarEvent = {
  id: 'event-1',
  calendar_id: 'work',
  calendar_name: 'Work',
  title: 'Planning session',
  start: '2026-09-05T09:00:00Z',
  end: '2026-09-05T10:00:00Z',
  all_day: false,
  location: 'Studio',
  etag: '"v1"',
  recurring: false,
}

describe('Nextcloud calendar', () => {
  it('starts the Nextcloud login flow without asking for a password', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/calendar/connection': { body: { connected: false, selected_calendars: 0 } },
      'POST /admin/api/calendar/connect': { body: { id: 'flow-1', login_url: 'https://cloud.example.com/login/flow' } },
    })
    renderApp('/admin/calendar')

    const user = userEvent.setup()
    await user.clear(await screen.findByLabelText(/nextcloud server/i))
    await user.type(screen.getByLabelText(/nextcloud server/i), 'https://cloud.example.com')
    await user.click(screen.getByRole('button', { name: /connect nextcloud/i }))

    expect(await screen.findByRole('link', { name: /open nextcloud/i })).toHaveAttribute('href', 'https://cloud.example.com/login/flow')
    expect(calls.find((call) => call.path === '/admin/api/calendar/connect')?.body).toEqual({ server_url: 'https://cloud.example.com' })
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument()
  })

  it('edits with an ETag and controls which calendars agents can access', async () => {
    const updated = { ...planning, title: 'Weekly planning', etag: '"v2"' }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/calendar/connection': { body: { connected: true, server_url: 'https://cloud.example.com', username: 'alex', selected_calendars: 1 } },
      'GET /admin/api/calendar/calendars': { body: { calendars } },
      'GET /admin/api/calendar/events': [{ body: { events: [planning] } }, { body: { events: [updated] } }],
      'GET /admin/api/calendar/events/event-1': { body: planning },
      'PUT /admin/api/calendar/events/event-1': { body: updated },
      'PUT /admin/api/calendar/calendars': { body: { selected: 2 } },
    })
    renderApp('/admin/calendar')

    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /planning session/i }))
    const dialog = await screen.findByRole('dialog')
    const title = within(dialog).getByLabelText(/^title$/i)
    await user.clear(title)
    await user.type(title, 'Weekly planning')
    await user.click(within(dialog).getByRole('button', { name: /save event/i }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(calls.find((call) => call.path === '/admin/api/calendar/events/event-1' && call.method === 'PUT')?.body).toMatchObject({ title: 'Weekly planning', etag: '"v1"' })

    await user.click(screen.getByRole('button', { name: /^calendars$/i }))
    const manager = screen.getByRole('region', { name: /agent-visible calendars/i })
    await user.click(within(manager).getByRole('checkbox', { name: /personal/i }))
    await user.click(within(manager).getByRole('button', { name: /save calendars/i }))

    await waitFor(() => expect(screen.queryByRole('region', { name: /agent-visible calendars/i })).not.toBeInTheDocument())
    expect(calls.find((call) => call.path === '/admin/api/calendar/calendars' && call.method === 'PUT')?.body).toEqual({ ids: ['work', 'personal'] })
  })
})
