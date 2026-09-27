import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { TableEntry } from '../api'
import { atlas, atlasDetail, authenticatedSession, beacon, mockApi, noteEntry, renderApp } from './helpers'

const owner = { read: false, starred: false, handled: false }
const now = new Date().toISOString()
const todo: TableEntry = { ...noteEntry, owner, id: '50', kind: 'todo', body: 'Add CSV export', created_at: now, project_name: 'Atlas', meta: { title: 'Add CSV export', tags: [], refs: [], origin: 'model' } }
const base = { 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/projects': { body: { projects: [atlas, beacon] } }, 'GET /admin/api/table/projects': { body: { projects: [], metadata: { total: 0, ready: 0, failed: 0, active: false } } } }

describe('undo, trash, and delete', () => {
  it('offers Undo on the toast of each quick action', async () => {
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/entries': { body: { entries: [todo], sources: [], tags: [] } },
      'POST /admin/api/entries/50/resolve': { status: 201, body: { ...noteEntry, kind: 'status', action_id: '901' } },
      'POST /admin/api/actions/901/undo': { body: { undone: true } },
    })
    renderApp('/admin/table?view=todos')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Mark done' }))
    const toast = (await screen.findByText('Todo marked done.')).closest('.toast') as HTMLElement
    await user.click(within(toast).getByRole('button', { name: 'Undo' }))
    expect(await screen.findByText('Undone.')).toBeInTheDocument()
    expect(calls.some((call) => call.method === 'POST' && call.path === '/admin/api/actions/901/undo')).toBe(true)
  })

  it('deletes an entry only after confirming, then offers Undo', async () => {
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/entries': { body: { entries: [todo], sources: [], tags: [] } },
      'GET /admin/api/entries/50/related': { body: { related: [] } },
      'DELETE /admin/api/entries/50': { body: { trash_id: '7', action_id: '902' } },
    })
    renderApp('/admin/table?view=todos')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Add CSV export' }))
    await user.click(screen.getByRole('button', { name: /delete entry/i }))
    const dialog = screen.getByRole('dialog', { name: 'Delete this entry?' })
    expect(calls.some((call) => call.method === 'DELETE')).toBe(false)
    await user.click(within(dialog).getByRole('button', { name: 'Delete entry' }))
    expect(await screen.findByText('Entry moved to Trash.')).toBeInTheDocument()
    expect(calls.some((call) => call.method === 'DELETE' && call.path === '/admin/api/entries/50')).toBe(true)
  })

  it('lists recent actions and undoes one of them', async () => {
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/actions': { body: { actions: [
        { id: '12', kind: 'owner', label: 'Starred: Release notes', project_slug: 'ai-news', created_at: now, undoable: true },
        { id: '11', kind: 'resolve', label: 'Marked done: Old task', project_slug: 'atlas', created_at: now, undone_at: now, undoable: false },
      ] } },
      'POST /admin/api/actions/12/undo': { body: { undone: true } },
    })
    renderApp('/admin/table?view=recent')
    const list = await screen.findByRole('region', { name: 'Recent actions' })
    const rows = within(list).getAllByRole('listitem')
    expect(within(rows[1]!).queryByRole('button', { name: 'Undo' })).not.toBeInTheDocument()
    expect(rows[1]).toHaveTextContent('Undone')
    await userEvent.setup().click(within(rows[0]!).getByRole('button', { name: 'Undo' }))
    expect(await screen.findByText('Undone.')).toBeInTheDocument()
    expect(calls.some((call) => call.path === '/admin/api/actions/12/undo')).toBe(true)
  })

  it('restores from trash and deletes forever only after confirming', async () => {
    const later = new Date(Date.now() + 20 * 86400000).toISOString()
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/trash': { body: { items: [
        { id: '5', kind: 'project', label: 'Beacon', project_slug: 'beacon', entry_count: 12, deleted_at: now, purge_at: later },
        { id: '4', kind: 'entry', label: 'Old note', project_slug: 'atlas', entry_count: 1, deleted_at: now, purge_at: later },
      ] } },
      'POST /admin/api/trash/5/restore': { body: { restored: true } },
      'DELETE /admin/api/trash/4': { status: 204 },
    })
    renderApp('/admin/table?view=trash')
    const list = await screen.findByRole('region', { name: 'Trash' })
    const [project, note] = within(list).getAllByRole('listitem')
    expect(project).toHaveTextContent('Project: Beacon')
    expect(project).toHaveTextContent('12 entries')
    expect(project).toHaveTextContent('gone in 20 days')
    const user = userEvent.setup()
    await user.click(within(project!).getByRole('button', { name: 'Restore' }))
    expect(await screen.findByText('Restored.')).toBeInTheDocument()
    await user.click(within(note!).getByRole('button', { name: 'Delete forever' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Delete forever?' })).getByRole('button', { name: 'Delete forever' }))
    expect(await screen.findByText('Deleted forever.')).toBeInTheDocument()
    expect(calls.some((call) => call.method === 'DELETE' && call.path === '/admin/api/trash/4')).toBe(true)
  })

  it('deletes a project only after its slug is typed', async () => {
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'GET /admin/api/projects/atlas/files': { body: { files: [] } },
      'GET /admin/api/projects/atlas/deletion': { body: { name: 'Atlas', entries: 187, handoffs: 2, files: 3 } },
      'DELETE /admin/api/projects/atlas': { body: { trash_id: '9', action_id: '903' } },
    })
    renderApp('/admin/projects/atlas')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /delete project/i }))
    const dialog = screen.getByRole('dialog', { name: 'Delete Atlas?' })
    expect(await within(dialog).findByText(/its 187 entries to Trash/)).toBeInTheDocument()
    const confirm = within(dialog).getByRole('button', { name: 'Delete project' })
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox'), 'atla')
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox'), 's')
    await user.click(confirm)
    expect(await screen.findByText('Atlas moved to Trash.')).toBeInTheDocument()
    expect(calls.find((call) => call.method === 'DELETE')?.body).toEqual({ confirm: 'atlas' })
  })
})
