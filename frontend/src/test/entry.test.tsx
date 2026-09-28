import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { TableEntry } from '../api'
import { authenticatedSession, mockApi, noteEntry, renderApp } from './helpers'

const owner = { read: false, starred: false, handled: false }
const now = new Date().toISOString()
const todo: TableEntry = {
  ...noteEntry, owner, id: '50', kind: 'todo', body: 'We should add CSV export to the table page because phones…', source: 'codex', created_at: now, project_name: 'Atlas',
  meta: { title: 'Add CSV export', gist: 'Phones need spreadsheets.', tags: ['export'], priority: 'high', refs: ['internal/admin/server.go'], origin: 'model', category: 'reports' },
}
const repeat: TableEntry = { ...todo, id: '52', source: 'claude-code', duplicate_of: '50', meta: { ...todo.meta!, title: 'Add CSV export again' } }

describe('entry page', () => {
  it('shows one entry whole: labels, full text, repeats, related entries, and its actions', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/entries/50': { body: { ...todo, repeats: [repeat] } },
      'GET /admin/api/entries/50/related': { body: { related: [{ ...todo, id: '7', project_name: 'Beacon', slug: 'beacon', similarity: 0.8, meta: { ...todo.meta!, title: 'Export design decision' } }] } },
      'POST /admin/api/entries/50/resolve': { status: 201, body: { ...noteEntry, kind: 'status', action_id: '901' } },
    })
    renderApp('/admin/entries/50')
    expect(await screen.findByRole('heading', { name: 'Add CSV export', level: 1 })).toBeInTheDocument()
    expect(screen.getByText('Phones need spreadsheets.')).toBeInTheDocument()
    expect(screen.getByText('high')).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Atlas' })[0]).toHaveAttribute('href', '/admin/projects/atlas')
    expect(screen.getByRole('link', { name: 'export' })).toHaveAttribute('href', '/admin/table?view=activity&tag=export')
    expect(within(screen.getByRole('region', { name: 'Full text' })).getByText(/phones…/)).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: /repeats/i })).getByRole('link', { name: 'Add CSV export again' })).toHaveAttribute('href', '/admin/entries/52')
    expect(await screen.findByRole('link', { name: 'Export design decision' })).toHaveAttribute('href', '/admin/entries/7')
    expect(within(screen.getByRole('region', { name: 'Labels' })).getByRole('button', { name: 'Edit labels' })).toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Mark done' }))
    expect(await screen.findByText('Todo marked done.')).toBeInTheDocument()
    expect(calls.some((call) => call.method === 'POST' && call.path === '/admin/api/entries/50/resolve')).toBe(true)
  })

  it('links a repeat to its original and a done todo to what closed it', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/entries/52': { body: { ...repeat, resolved_by: { entry_id: '60', origin: 'model', created_at: now }, repeats: [] } },
      'GET /admin/api/entries/52/related': { body: { related: [] } },
    })
    renderApp('/admin/entries/52')
    expect(await screen.findByRole('link', { name: 'an earlier entry' })).toHaveAttribute('href', '/admin/entries/50')
    expect(screen.getByRole('link', { name: /an agent's update/i })).toHaveAttribute('href', '/admin/entries/60')
    expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument()
  })

  it('explains a missing entry and points to Trash', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/entries/99': { status: 404, body: { error: 'entry not found' } } })
    renderApp('/admin/entries/99')
    expect(await screen.findByText(/doesn't exist, or it was deleted/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Trash' })).toHaveAttribute('href', '/admin/table?view=trash')
  })
})
