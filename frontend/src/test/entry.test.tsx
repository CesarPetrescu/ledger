import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { HistoryEvent, TableEntry } from '../api'
import { authenticatedSession, mockApi, noteEntry, renderApp } from './helpers'

const owner = { read: false, starred: false, handled: false }
const now = new Date().toISOString()
const todo: TableEntry = {
  ...noteEntry, owner, id: '50', kind: 'todo', body: 'We should add CSV export to the table page because phones…', source: 'codex', created_at: now, project_name: 'Atlas',
  context: 'repo ledger, branch table-export',
  meta: { title: 'Add CSV export', gist: 'Phones need spreadsheets.', tags: ['export'], priority: 'high', refs: ['internal/admin/server.go'], origin: 'model', category: 'reports' },
}
const repeat: TableEntry = { ...todo, id: '52', source: 'claude-code', duplicate_of: '50', meta: { ...todo.meta!, title: 'Add CSV export again' } }
const history: HistoryEvent[] = [
  { at: now, kind: 'created', actor: 'codex', text: 'Codex CLI' },
  { at: now, kind: 'repeat', actor: 'claude-code', text: 'Add CSV export again', entry_id: '52' },
  { at: now, kind: 'action', actor: 'ledger-admin', text: 'Snoozed 1 day', undone: true },
  { at: now, kind: 'reply', actor: 'ledger-admin', text: 'Use semicolons for Excel.', entry_id: '61' },
]
const ask: TableEntry = { ...noteEntry, owner, id: '70', kind: 'note', source: 'claude-code', created_at: now, project_name: 'Atlas', meta: { title: 'Pricing', tags: [], refs: [], origin: 'model', ask: 'Confirm the pricing claims' } }

describe('entry page', () => {
  it('shows one entry whole: labels, full text, where it came from, its history, related entries, and its actions', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/entries/50': { body: { ...todo, repeats: [repeat], repeats_total: 3 } },
      'GET /admin/api/entries/50/history': { body: { history } },
      'GET /admin/api/entries/50/related': { body: { related: [{ ...todo, id: '7', project_name: 'Beacon', slug: 'beacon', similarity: 0.8, meta: { ...todo.meta!, title: 'Export design decision' } }] } },
      'POST /admin/api/entries/50/resolve': { status: 201, body: { ...noteEntry, kind: 'status', action_id: '901' } },
    })
    renderApp('/admin/entries/50')
    expect(await screen.findByRole('heading', { name: 'Add CSV export', level: 2 })).toBeInTheDocument()
    expect(screen.getByText('Phones need spreadsheets.')).toBeInTheDocument()
    expect(screen.getByText('high')).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Atlas' })[0]).toHaveAttribute('href', '/admin/projects/atlas')
    expect(screen.getByRole('link', { name: 'export' })).toHaveAttribute('href', '/admin/table?view=activity&tag=export')
    expect(screen.getByText(/phones…/)).toBeInTheDocument()
    const timeline = within(await screen.findByRole('region', { name: 'History' }))
    expect(timeline.getByText('repo ledger, branch table-export')).toBeInTheDocument()
    expect(await timeline.findByText('Codex CLI')).toBeInTheDocument()
    expect(timeline.getByRole('link', { name: 'wrote it again' })).toHaveAttribute('href', '/admin/entries/52')
    expect(timeline.getByText('(undone)')).toBeInTheDocument()
    expect(timeline.getByText('Use semicolons for Excel.')).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Export design decision' })).toHaveAttribute('href', '/admin/entries/7')
    expect(screen.getByRole('button', { name: 'Edit labels' })).toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Mark done' }))
    expect(await screen.findByText('Todo marked done.')).toBeInTheDocument()
    expect(calls.some((call) => call.method === 'POST' && call.path === '/admin/api/entries/50/resolve')).toBe(true)
  })

  it('answers a question: the reply is saved under it and marking it handled can be undone', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/entries/70': { body: { ...ask, repeats: [], repeats_total: 0 } },
      'GET /admin/api/entries/70/history': { body: { history: [{ at: now, kind: 'created', actor: 'claude-code', text: '' }] } },
      'GET /admin/api/entries/70/related': { body: { related: [] } },
      'POST /admin/api/entries/70/replies': { status: 201, body: { ...noteEntry, id: '71', reply_to: '70', action_id: '905' } },
    })
    renderApp('/admin/entries/70')
    const question = await screen.findByRole('region', { name: 'Question for you' })
    expect(question).toHaveTextContent('claude-code asks you')
    expect(question).toHaveTextContent('Confirm the pricing claims')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send answer' }))
    expect(screen.getByRole('alert')).toHaveTextContent('Write a reply first.')
    await user.type(screen.getByLabelText('Answer claude-code'), '10% is right')
    await user.click(screen.getByRole('button', { name: 'Send answer' }))
    expect(await screen.findByText('Reply saved; marked handled.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument()
    expect(calls.find((call) => call.path === '/admin/api/entries/70/replies')?.body).toEqual({ body: '10% is right' })
    expect(screen.getByLabelText('Answer claude-code')).toHaveValue('')
  })

  it('links a repeat to its original and a done todo to what closed it', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/entries/52': { body: { ...repeat, resolved_by: { entry_id: '60', origin: 'model', created_at: now }, repeats: [], repeats_total: 0 } },
      'GET /admin/api/entries/52/history': { body: { history: [] } },
      'GET /admin/api/entries/52/related': { body: { related: [] } },
    })
    renderApp('/admin/entries/52')
    expect(await screen.findByRole('link', { name: 'an earlier entry' })).toHaveAttribute('href', '/admin/entries/50')
    expect(screen.getByRole('link', { name: /an agent's update/i })).toHaveAttribute('href', '/admin/entries/60')
    expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument()
  })

  it('leaves the page for its project after the entry is deleted', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/entries/50': { body: { ...todo, repeats: [], repeats_total: 0 } },
      'GET /admin/api/entries/50/history': { body: { history: [] } },
      'GET /admin/api/entries/50/related': { body: { related: [] } },
      'DELETE /admin/api/entries/50': { body: { trash_id: '7', action_id: '903' } },
      'GET /admin/api/projects': { body: { projects: [] } },
      'GET /admin/api/table/projects': { body: { projects: [], metadata: { total: 0, ready: 0, failed: 0, active: false } } },
      'GET /admin/api/projects/atlas': { status: 404, body: { error: 'project not found' } },
    })
    renderApp('/admin/entries/50')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /delete entry/i }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete entry' }))
    expect(await screen.findByText('Entry moved to Trash.')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/admin/projects/atlas')
  })

  it('explains a missing entry and points to Trash', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/entries/99': { status: 404, body: { error: 'entry not found' } } })
    renderApp('/admin/entries/99')
    expect(await screen.findByText(/doesn't exist, or it was deleted/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Trash' })).toHaveAttribute('href', '/admin/table?view=trash')
  })
})
