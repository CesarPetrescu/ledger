import { screen, within } from '@testing-library/react'
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

  it('snoozes a question and an open todo from their rows, with the same choices as the entry panel', async () => {
    const { calls } = mockApi({ ...base, 'POST /admin/api/entries/70/owner': { body: { ...owner, snoozed_until: '2026-01-04', action_id: '906' } }, 'POST /admin/api/entries/50/owner': { body: { ...owner, snoozed_until: '2026-01-02', action_id: '907' } } })
    renderApp('/admin/')
    const asks = await screen.findByRole('region', { name: 'Needs you' })
    const snooze = within(asks).getByRole('combobox', { name: 'Snooze' })
    expect(within(snooze).getAllByRole('option').map((option) => option.textContent)).toEqual(['Snooze…', 'Until tomorrow', 'For 3 days', 'For a week'])
    const user = userEvent.setup()
    await user.selectOptions(snooze, '3')
    expect(await screen.findByText('Snoozed for 3 days.')).toBeInTheDocument()
    expect(calls.find((call) => call.path === '/admin/api/entries/70/owner')?.body).toEqual({ snooze_days: 3 })

    const todos = screen.getByRole('region', { name: 'Todos' })
    expect(within(todos).getByRole('button', { name: 'Mark done' })).toBeInTheDocument()
    // A todo asks nothing, so it has no Answer.
    expect(within(todos).queryByRole('button', { name: 'Answer' })).not.toBeInTheDocument()
    await user.selectOptions(within(todos).getByRole('combobox', { name: 'Snooze' }), '1')
    expect(await screen.findByText('Snoozed until tomorrow.')).toBeInTheDocument()
    expect(calls.find((call) => call.path === '/admin/api/entries/50/owner')?.body).toEqual({ snooze_days: 1 })
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
