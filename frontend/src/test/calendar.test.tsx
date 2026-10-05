import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { CalendarEvent, CalendarSource } from '../api'
import { authenticatedSession, mockApi, renderApp } from './helpers'

const calendars: CalendarSource[] = [
  { id: 'work', name: 'Work', description: 'Shared schedule', selected: true },
  { id: 'personal', name: 'Personal', selected: false },
]

// Dated today: the calendar opens on the current month, whenever the tests run.
const todayAt = (hour: number) => { const date = new Date(); date.setHours(hour, 0, 0, 0); return date.toISOString() }
const planning: CalendarEvent = {
  id: 'event-1',
  calendar_id: 'work',
  calendar_name: 'Work',
  title: 'Planning session',
  start: todayAt(9),
  end: todayAt(10),
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

    await user.click(screen.getByRole('button', { name: /add event/i }))
    const newEvent = await screen.findByRole('dialog')
    await user.click(within(newEvent).getByLabelText(/all-day event/i))
    const starts = within(newEvent).getByLabelText(/starts/i) as HTMLInputElement
    const ends = within(newEvent).getByLabelText(/ends/i) as HTMLInputElement
    expect(starts.type).toBe('date')
    expect(ends.value > starts.value).toBe(true)
    await user.click(within(newEvent).getByRole('button', { name: /cancel/i }))
  })

  it('does not offer event creation until a calendar is selected', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/calendar/connection': { body: { connected: true, server_url: 'https://cloud.example.com', username: 'alex', selected_calendars: 0 } },
      'GET /admin/api/calendar/calendars': { body: { calendars: calendars.map((calendar) => ({ ...calendar, selected: false })) } },
      'GET /admin/api/calendar/events': { body: { events: [] } },
    })
    renderApp('/admin/calendar')

    expect(await screen.findByRole('region', { name: /agent-visible calendars/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /add event/i })).not.toBeInTheDocument()
  })
})

describe('calendar views', () => {
  const day = (offset: number) => {
    const date = new Date()
    date.setDate(date.getDate() + offset)
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
  }
  const todo = { id: '50', slug: 'atlas', kind: 'todo', body: 'Send the invoice', source: 'codex', client_id: 'c', created_at: new Date().toISOString(), project_name: 'Atlas',
    owner: { read: false, starred: false, handled: false }, meta: { title: 'Send the invoice', tags: [], refs: [], origin: 'model', due: day(0) } }

  it('shows todos due and project deadlines without Nextcloud, and opens a todo beside the calendar', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/calendar/connection': { body: { connected: false, selected_calendars: 0 } },
      'GET /admin/api/entries': (_init, url) => ({ body: { entries: url.searchParams.get('wakes_from') ? [] : [todo], sources: [], tags: [] } }),
      'GET /admin/api/projects': { body: { projects: [{ slug: 'atlas', name: 'Atlas', tier: 'focus', hours_wk: 4, type: '', description: '', goal: '', deadline: day(0), needs_me: '', automate: '', stack: '', updated_at: '' }] } },
      'GET /admin/api/entries/50': { body: { ...todo, repeats: [], repeats_total: 0 } },
      'GET /admin/api/entries/50/history': { body: { history: [] } },
      'GET /admin/api/entries/50/related': { body: { related: [] } },
    })
    renderApp('/admin/calendar')
    const month = await screen.findByRole('grid')
    expect(await within(month).findByRole('button', { name: /send the invoice/i })).toBeInTheDocument()
    expect(within(month).getByRole('button', { name: /^atlas$/i })).toBeInTheDocument()
    const due = calls.find((call) => call.path === '/admin/api/entries' && call.url.searchParams.get('kind') === 'todo')!
    expect(due.url.searchParams.get('due_from')).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    expect(due.url.searchParams.get('due_before')).toMatch(/^\d{4}-\d{2}-\d{2}$/)

    const user = userEvent.setup()
    await user.click(within(month).getByRole('button', { name: /send the invoice/i }))
    expect(await within(await screen.findByRole('complementary', { name: 'Entry' })).findByRole('heading', { name: 'Send the invoice' })).toBeInTheDocument()

    await user.click(screen.getByRole('radio', { name: 'Week' }))
    expect(await screen.findByRole('region', { name: new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'short' }).format(new Date()) })).toHaveTextContent('Send the invoice')
    await user.click(screen.getByRole('radio', { name: 'Agenda' }))
    expect(screen.getByText('Todo due')).toBeInTheDocument()
  })
})

it('shows an event on every day it covers, even when it started before the view', async () => {
  const day = (offset: number) => {
    const date = new Date()
    date.setDate(date.getDate() + offset)
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
  }
  const trip: CalendarEvent = { ...planning, id: 'trip', title: 'Conference trip', all_day: true, start: day(-40), end: day(2) }
  mockApi({
    'GET /admin/api/session': authenticatedSession,
    'GET /admin/api/calendar/connection': { body: { connected: true, server_url: 'https://cloud.example.com', username: 'alex', selected_calendars: 1 } },
    'GET /admin/api/calendar/calendars': { body: { calendars } },
    'GET /admin/api/calendar/events': { body: { events: [trip] } },
    'GET /admin/api/entries': { body: { entries: [], sources: [], tags: [] } },
    'GET /admin/api/projects': { body: { projects: [] } },
  })
  renderApp('/admin/calendar')
  const user = userEvent.setup()
  await user.click(await screen.findByRole('radio', { name: 'Week' }))
  const today = await screen.findByRole('region', { name: new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'short' }).format(new Date()) })
  expect(await within(today).findByText('Conference trip')).toBeInTheDocument()
  expect(within(today).getByText('Continues')).toBeInTheDocument()
})

it('leaves nothing of the connection behind after disconnecting', async () => {
  mockApi({
    'GET /admin/api/session': authenticatedSession,
    'GET /admin/api/calendar/connection': [{ body: { connected: true, server_url: 'https://cloud.example.com', username: 'alex', selected_calendars: 1 } }, { body: { connected: false, selected_calendars: 0 } }],
    'GET /admin/api/calendar/calendars': { body: { calendars } },
    'GET /admin/api/calendar/events': { body: { events: [] } },
    'DELETE /admin/api/calendar/connection': { body: { disconnected: true } },
    'GET /admin/api/entries': { body: { entries: [], sources: [], tags: [] } },
    'GET /admin/api/projects': { body: { projects: [] } },
  })
  renderApp('/admin/calendar')
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: /^calendars$/i }))
  await user.click(screen.getByRole('button', { name: /disconnect nextcloud/i }))
  await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /^disconnect$/i }))
  expect(await screen.findByText(/connect a nextcloud calendar/i)).toBeInTheDocument()
  expect(screen.queryByRole('region', { name: /agent-visible calendars/i })).not.toBeInTheDocument()
})

it('says so when Nextcloud calendars cannot be listed, with a retry', async () => {
  const { calls } = mockApi({
    'GET /admin/api/session': authenticatedSession,
    'GET /admin/api/calendar/connection': { body: { connected: true, server_url: 'https://cloud.example.com', username: 'alex', selected_calendars: 1 } },
    'GET /admin/api/calendar/calendars': [{ status: 502, body: { error: 'nextcloud unavailable' } }, { body: { calendars } }],
    'GET /admin/api/calendar/events': { body: { events: [planning] } },
    'GET /admin/api/entries': { body: { entries: [], sources: [], tags: [] } },
    'GET /admin/api/projects': { body: { projects: [] } },
  })
  renderApp('/admin/calendar')
  expect(await screen.findByText(/couldn't reach your nextcloud calendars/i)).toBeInTheDocument()
  await userEvent.setup().click(screen.getByRole('button', { name: /retry/i }))
  await waitFor(() => expect(calls.filter((call) => call.path === '/admin/api/calendar/calendars')).toHaveLength(2))
  expect(screen.queryByText(/couldn't reach your nextcloud calendars/i)).not.toBeInTheDocument()
})
