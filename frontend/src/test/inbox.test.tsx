import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { TableEntry } from '../api'
import { authenticatedSession, mockApi, noSummaries, noteEntry, renderApp } from './helpers'

const owner = { read: false, starred: false, handled: false }
const now = new Date().toISOString()
const ask: TableEntry = { ...noteEntry, owner, id: '70', source: 'codex', created_at: now, project_name: 'Atlas', meta: { title: 'Bucket question', tags: [], refs: [], origin: 'model', ask: 'Which bucket should uploads use?' } }
const todo: TableEntry = { ...noteEntry, owner, id: '50', kind: 'todo', body: 'Add CSV export', created_at: now, project_name: 'Atlas', meta: { title: 'Add CSV export', tags: [], refs: [], origin: 'model' } }
const base = {
  'GET /admin/api/session': authenticatedSession,
  'GET /admin/api/table/projects': { body: noSummaries },
  'GET /admin/api/inbox': { body: { needs_you: [ask], todos: [todo], todos_total: 1, projects: [] } },
}

describe('inbox actions', () => {
  it('answers a question inline: the reply is saved under it and marks it handled, with Undo', async () => {
    const { calls } = mockApi({ ...base, 'POST /admin/api/entries/70/replies': { status: 201, body: { ...noteEntry, id: '71', reply_to: '70', action_id: '905' } } })
    renderApp('/admin/')
    const asks = await screen.findByRole('region', { name: 'Needs you' })
    const answer = within(asks).getByRole('button', { name: 'Answer' })
    expect(within(asks).getByRole('button', { name: 'Handled' })).toBeInTheDocument()
    expect(answer).toHaveAttribute('aria-expanded', 'false')
    expect(within(asks).queryByRole('textbox')).not.toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(answer)
    expect(answer).toHaveAttribute('aria-expanded', 'true')
    const box = within(asks).getByLabelText('Answer codex')
    expect(box).toHaveFocus()
    await user.type(box, 'Use the staging bucket')
    await user.click(within(asks).getByRole('button', { name: 'Send answer' }))
    const toast = (await screen.findByText('Reply saved; marked handled.')).closest('.toast') as HTMLElement
    expect(within(toast).getByRole('button', { name: 'Undo' })).toBeInTheDocument()
    expect(calls.find((call) => call.path === '/admin/api/entries/70/replies')?.body).toEqual({ body: 'Use the staging bucket' })
  })

  it('keeps a typed answer when its box is closed and opened again', async () => {
    mockApi(base)
    renderApp('/admin/')
    const asks = await screen.findByRole('region', { name: 'Needs you' })
    const answer = within(asks).getByRole('button', { name: 'Answer' })
    const user = userEvent.setup()
    await user.click(answer)
    await user.type(within(asks).getByLabelText('Answer codex'), 'Half an answer')
    await user.click(answer)
    expect(answer).toHaveAttribute('aria-expanded', 'false')
    expect(within(asks).getByLabelText('Answer codex')).not.toBeVisible()
    await user.click(answer)
    const box = within(asks).getByLabelText('Answer codex')
    expect(box).toBeVisible()
    expect(box).toHaveFocus()
    expect(box).toHaveValue('Half an answer')
  })

  it('moves focus to the next row when the one you acted on leaves the list', async () => {
    const second: TableEntry = { ...ask, id: '72', meta: { ...ask.meta!, title: 'Region question', ask: 'Which region should Atlas run in?' } }
    mockApi({
      ...base,
      'GET /admin/api/inbox': [{ body: { needs_you: [ask, second], todos: [todo], todos_total: 1, projects: [] } }, { body: { needs_you: [second], todos: [todo], todos_total: 1, projects: [] } }],
      'POST /admin/api/entries/70/owner': { body: { ...owner, handled: true, action_id: '908' } },
    })
    renderApp('/admin/')
    const asks = await screen.findByRole('region', { name: 'Needs you' })
    const user = userEvent.setup()
    await user.click(within(asks).getAllByRole('button', { name: 'Handled' })[0]!)
    await waitFor(() => expect(within(asks).queryByText('Which bucket should uploads use?')).not.toBeInTheDocument())
    await waitFor(() => expect(within(asks).getByRole('button', { name: 'Which region should Atlas run in?' })).toHaveFocus())
  })

  it('snoozes a question and an open todo from their rows, with the same choices as the entry panel', async () => {
    const { calls } = mockApi({ ...base, 'POST /admin/api/entries/70/owner': { body: { ...owner, snoozed_until: '2026-01-04', action_id: '906' } }, 'POST /admin/api/entries/50/owner': { body: { ...owner, snoozed_until: '2026-01-02', action_id: '907' } } })
    renderApp('/admin/')
    const asks = await screen.findByRole('region', { name: 'Needs you' })
    const user = userEvent.setup()
    await user.click(within(asks).getByRole('button', { name: 'Snooze' }))
    const menu = within(asks).getByRole('menu', { name: 'Snooze' })
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['Until tomorrow', 'For 3 days', 'For a week'])
    // Arrow keys move through the choices; nothing is snoozed until one is picked.
    await user.keyboard('{ArrowDown}')
    expect(within(menu).getByRole('menuitem', { name: 'For 3 days' })).toHaveFocus()
    expect(calls.some((call) => call.path === '/admin/api/entries/70/owner')).toBe(false)
    await user.keyboard('{Enter}')
    expect(await screen.findByText('Snoozed for 3 days.')).toBeInTheDocument()
    expect(calls.find((call) => call.path === '/admin/api/entries/70/owner')?.body).toEqual({ snooze_days: 3 })

    const todos = screen.getByRole('region', { name: 'Todos' })
    expect(within(todos).getByRole('button', { name: 'Mark done' })).toBeInTheDocument()
    // A todo asks nothing, so it has no Answer.
    expect(within(todos).queryByRole('button', { name: 'Answer' })).not.toBeInTheDocument()
    await user.click(within(todos).getByRole('button', { name: 'Snooze' }))
    await user.click(within(todos).getByRole('menuitem', { name: 'Until tomorrow' }))
    expect(await screen.findByText('Snoozed until tomorrow.')).toBeInTheDocument()
    expect(calls.find((call) => call.path === '/admin/api/entries/50/owner')?.body).toEqual({ snooze_days: 1 })
  })

  it('offers Snooze again once a snooze has ended, not Wake now', async () => {
    const yesterday = new Date()
    yesterday.setDate(yesterday.getDate() - 1)
    const ended = `${yesterday.getFullYear()}-${String(yesterday.getMonth() + 1).padStart(2, '0')}-${String(yesterday.getDate()).padStart(2, '0')}`
    const woken = { ...owner, snoozed_until: ended }
    mockApi({ ...base, 'GET /admin/api/inbox': { body: { needs_you: [{ ...ask, owner: woken }], todos: [{ ...todo, owner: woken }], todos_total: 1, projects: [] } } })
    renderApp('/admin/')
    for (const name of ['Needs you', 'Todos']) {
      const group = await screen.findByRole('region', { name })
      expect(within(group).getByRole('button', { name: 'Snooze' })).toBeInTheDocument()
      expect(within(group).queryByRole('button', { name: 'Wake now' })).not.toBeInTheDocument()
    }
  })

  it('trusts the server on whether a snooze has ended, not the browser’s calendar', async () => {
    const day = (offset: number) => {
      const date = new Date()
      date.setDate(date.getDate() + offset)
      return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
    }
    // The server's day is behind the browser's: a snooze ending "today" here is still on there.
    const stillOn = { ...owner, snoozed_until: day(0), snoozed: true }
    // And ahead of it: a snooze ending "tomorrow" here has already ended there.
    const ended = { ...owner, snoozed_until: day(1), snoozed: false }
    mockApi({ ...base, 'GET /admin/api/inbox': { body: { needs_you: [{ ...ask, owner: stillOn }], todos: [{ ...todo, owner: ended }], todos_total: 1, projects: [] } } })
    renderApp('/admin/')
    const asks = await screen.findByRole('region', { name: 'Needs you' })
    expect(within(asks).getByRole('button', { name: 'Wake now' })).toBeInTheDocument()
    const todos = screen.getByRole('region', { name: 'Todos' })
    expect(within(todos).getByRole('button', { name: 'Snooze' })).toBeInTheDocument()
    expect(within(todos).queryByRole('button', { name: 'Wake now' })).not.toBeInTheDocument()
  })

  it('keeps the open entry while you type an inline answer, beside the panel’s own answer box', async () => {
    mockApi({
      ...base,
      'GET /admin/api/entries/70': { body: { ...ask, repeats: [], repeats_total: 0 } },
      'GET /admin/api/entries/70/history': { body: { history: [] } },
      'GET /admin/api/entries/70/related': { body: { related: [] } },
    })
    renderApp('/admin/?entry=70')
    const panel = await screen.findByRole('complementary', { name: 'Entry' })
    await within(panel).findByRole('heading', { name: 'Bucket question', level: 2 })
    const asks = screen.getByRole('region', { name: 'Needs you' })
    const user = userEvent.setup()
    await user.click(within(asks).getByRole('button', { name: 'Answer' }))
    const inline = within(asks).getByLabelText('Answer codex')
    // Two boxes for one entry: each label still names its own textarea.
    expect(within(panel).getByLabelText('Answer codex')).not.toBe(inline)
    await user.type(inline, 'first{ArrowDown}{ArrowUp}{Escape} second')
    expect(inline).toHaveValue('first second')
    expect(window.location.search).toBe('?entry=70')
    expect(screen.getByRole('complementary', { name: 'Entry' })).toBeInTheDocument()
  })
})
