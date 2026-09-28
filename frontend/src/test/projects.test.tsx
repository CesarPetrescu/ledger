import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { ProjectSummary, TableEntry } from '../api'
import { atlas, atlasDetail, authenticatedSession, beacon, decisionEntry, mockApi, noSummaries, renderApp } from './helpers'

const projectBase = {
  'GET /admin/api/session': authenticatedSession,
  'GET /admin/api/table/projects': { body: noSummaries },
  'GET /admin/api/entries': { body: { entries: [], sources: [], tags: [] } },
}
const atlasSummary: ProjectSummary = {
  slug: 'atlas', name: 'Atlas', tier: 'focus', deadline: 'Friday', needs_me: 'Review the migration', open_todos: 2, week_entries: 5, week_agents: ['codex'],
  status_title: 'Deployed table page', status_body: '', status_source: 'codex', digest: 'Shipped the table page; two todos remain.', status_state: 'in_progress', status_detail: '', needs_you: 1,
}
const labelled: TableEntry = {
  ...decisionEntry, project_name: 'Atlas', owner: { read: false, starred: false, handled: false },
  meta: { title: 'Use PostgreSQL everywhere', tags: [], refs: [], origin: 'model' },
}

describe('project browser', () => {
  it('lists projects densely, filters by text and tier, and opens a project page with AI titles instead of raw text', async () => {
    const { calls } = mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [atlas, beacon] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'GET /admin/api/table/projects': { body: { ...noSummaries, projects: [atlasSummary] } },
      'GET /admin/api/entries': { body: { entries: [labelled], sources: ['agent-one'], tags: [] } },
    })
    renderApp('/admin/projects')
    const list = await screen.findByRole('list', { name: /projects/i })
    expect(within(list).getAllByRole('listitem')).toHaveLength(2)
    expect(within(list).getByText('1 for you')).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/filter projects/i), 'bea')
    expect(within(list).getAllByRole('listitem')).toHaveLength(1)
    expect(within(list).getByText('Beacon')).toBeInTheDocument()
    await user.clear(screen.getByLabelText(/filter projects/i))
    await user.click(screen.getByRole('radio', { name: /^focus$/i }))
    expect(within(list).getAllByRole('listitem')).toHaveLength(1)
    expect(within(list).getByText('Atlas')).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: /^park$/i }))
    await user.type(screen.getByLabelText(/filter projects/i), 'zzz')
    expect(screen.getByText(/no projects match/i)).toBeInTheDocument()
    await user.clear(screen.getByLabelText(/filter projects/i))
    await user.click(screen.getByRole('radio', { name: /^all$/i }))
    await user.click(within(screen.getByRole('list', { name: /projects/i })).getByRole('link', { name: /atlas/i }))
    expect(await screen.findByRole('heading', { name: 'Atlas', level: 1 })).toBeInTheDocument()
    const status = screen.getByRole('region', { name: 'Project status' })
    expect(status).toHaveTextContent('Shipped the table page; two todos remain.')
    expect(within(status).getByRole('link', { name: /1\s*question waits for you/i })).toHaveAttribute('href', '/admin/')
    expect(within(status).getByRole('link', { name: /open todo/i })).toHaveAttribute('href', '/admin/projects/atlas/todos')
    expect(screen.getByRole('link', { name: 'Activity', current: 'page' })).toBeInTheDocument()
    // Rows show the AI title and a link to the entry, not the raw body.
    expect(await screen.findByRole('button', { name: 'Use PostgreSQL everywhere' })).toBeInTheDocument()
    expect(screen.queryByText('Use PostgreSQL <b>everywhere</b>.')).not.toBeInTheDocument()
    expect(Object.fromEntries(calls.find((call) => call.path === '/admin/api/entries')!.url.searchParams)).toMatchObject({ project: 'atlas' })
    expect(screen.queryByRole('combobox', { name: /filter by project/i })).not.toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: 'Details' }))
    const meta = await screen.findByRole('list', { name: /project details/i })
    expect(within(meta).getByText('Goal').nextElementSibling).toHaveTextContent('Ship the operator console')
    expect(within(meta).getByText('Deadline').nextElementSibling).toHaveTextContent('Friday')
  })

  it('adds an entry from the project page without asking for the project', async () => {
    const { calls } = mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'POST /admin/api/projects/atlas/entries': { status: 201, body: { id: '42', slug: 'atlas', kind: 'todo', body: 'Write the runbook', source: 'ledger-admin', client_id: 'admin-session-0123456789ab', created_at: '2026-09-04T08:00:00Z' } },
    })
    renderApp('/admin/projects/atlas/todos')
    await screen.findByRole('heading', { name: 'Atlas', level: 1 })
    const user = userEvent.setup()
    await user.click(await screen.findByText('Add a todo'))
    const form = screen.getByRole('form', { name: /add entry/i })
    expect(within(form).queryByRole('combobox', { name: /project/i })).not.toBeInTheDocument()
    await user.type(within(form).getByRole('textbox', { name: /text/i }), 'Write the runbook')
    await user.click(within(form).getByRole('button', { name: /add/i }))
    expect(await screen.findByText('Entry added.')).toBeInTheDocument()
    expect(calls.find((call) => call.method === 'POST')?.body).toEqual({ kind: 'todo', body: 'Write the runbook' })
    expect(calls.find((call) => call.path === '/admin/api/projects/atlas')?.url.searchParams.get('entries')).toBe('1')
  })

  it('shows a new status by its own text until the AI titles it', async () => {
    mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'GET /admin/api/table/projects': { body: { ...noSummaries, projects: [{ ...atlasSummary, digest: '', status_title: '', status_body: 'Deployed the new build', status_at: '2026-09-03T12:00:00Z' }] } },
    })
    renderApp('/admin/projects/atlas')
    const status = await screen.findByRole('region', { name: 'Project status' })
    expect(within(status).getByText('Latest status')).toBeInTheDocument()
    expect(within(status).getByText('Deployed the new build')).toBeInTheDocument()
  })

  it('says so when the weekly summary fails to load, with a retry', async () => {
    mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/table/projects': { status: 500, body: { error: 'hidden' } },
    })
    renderApp('/admin/projects')
    expect(await screen.findByText(/couldn't load this week's project summary/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })

  it('says so on a project page when its summary fails to load', async () => {
    mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'GET /admin/api/table/projects': { status: 500, body: { error: 'hidden' } },
    })
    renderApp('/admin/projects/atlas')
    expect(await screen.findByText(/couldn't load this project's week/i)).toBeInTheDocument()
  })

  it('keeps the add form on a project page when the project list fails to load', async () => {
    mockApi({
      ...projectBase,
      'GET /admin/api/projects': { status: 500, body: { error: 'hidden' } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
    })
    renderApp('/admin/projects/atlas')
    await screen.findByRole('heading', { name: 'Atlas', level: 1 })
    expect(await screen.findByText('Add an entry')).toBeInTheDocument()
  })

  it('creates a project through the form and shows server validation errors', async () => {
    const { calls } = mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [] } },
      'PUT /admin/api/projects/orbit': [
        { status: 400, body: { error: 'hours_wk must be between 0 and 168' } },
        { body: { ...beacon, slug: 'orbit', name: 'Orbit', tier: 'maintain', hours_wk: 4 } },
      ],
      'GET /admin/api/projects/orbit': { body: { project: { ...beacon, slug: 'orbit', name: 'Orbit', tier: 'maintain', hours_wk: 4 }, entries: [] } },
    })
    renderApp('/admin/projects/_new')
    const user = userEvent.setup()
    const form = await screen.findByRole('form', { name: /new project/i })
    await user.type(within(form).getByLabelText(/^slug/i), 'orbit')
    await user.type(within(form).getByLabelText(/^name/i), 'Orbit')
    await user.selectOptions(within(form).getByLabelText(/^tier/i), 'maintain')
    await user.clear(within(form).getByLabelText(/hours per week/i))
    await user.type(within(form).getByLabelText(/hours per week/i), '4')
    await user.click(within(form).getByRole('button', { name: /create project/i }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('hours_wk must be between 0 and 168')
    await user.click(within(form).getByRole('button', { name: /create project/i }))
    expect(await screen.findByRole('heading', { name: 'Orbit', level: 1 })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(/project saved/i)
    const put = calls.filter((call) => call.method === 'PUT').at(-1)
    expect(put?.body).toMatchObject({ name: 'Orbit', tier: 'maintain', hours_wk: 4 })
    expect(put?.body).not.toHaveProperty('slug')
    expect(await screen.findByText('No entries match.')).toBeInTheDocument()
  })

  it('edits an existing project in place', async () => {
    const savedAtlas = Object.fromEntries(Object.entries({ ...atlas, goal: 'Ship v2' }).filter(([key]) => key !== 'last_entry_at'))
    const { calls } = mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'PUT /admin/api/projects/atlas': { body: savedAtlas },
    })
    renderApp('/admin/projects/atlas/details')
    await screen.findByRole('heading', { name: 'Atlas', level: 1 })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /edit project/i }))
    const form = screen.getByRole('form', { name: /edit project/i })
    expect(within(form).queryByLabelText(/^slug/i)).not.toBeInTheDocument()
    await user.clear(within(form).getByLabelText(/^goal/i))
    await user.type(within(form).getByLabelText(/^goal/i), 'Ship v2')
    await user.click(within(form).getByRole('button', { name: /save changes/i }))
    expect(await screen.findByRole('status')).toHaveTextContent(/project saved/i)
    const meta = screen.getByRole('list', { name: /project details/i })
    expect(within(meta).getByText('Goal').nextElementSibling).toHaveTextContent('Ship v2')
    const body = calls.find((call) => call.method === 'PUT')?.body
    expect(body).toMatchObject({ goal: 'Ship v2', name: 'Atlas' })
    expect(body).not.toHaveProperty('slug')
    expect(body).not.toHaveProperty('updated_at')
    expect(body).not.toHaveProperty('last_entry_at')
    expect(within(screen.getByRole('list', { name: /projects/i })).getByRole('link', { name: /atlas/i }).querySelector('time')).toHaveAttribute('datetime', atlas.last_entry_at)
  })

  it('opens a project whose slug is new instead of the create form', async () => {
    const newProject = { ...atlas, slug: 'new', name: 'New Project' }
    mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [newProject] } },
      'GET /admin/api/projects/new': { body: { project: newProject, entries: [] } },
    })
    renderApp('/admin/projects/new')
    expect(await screen.findByRole('heading', { name: 'New Project', level: 1 })).toBeInTheDocument()
    expect(screen.queryByRole('form', { name: /new project/i })).not.toBeInTheDocument()
  })

  it('shows a not-found state for unknown projects', async () => {
    mockApi({
      ...projectBase,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/ghost': { status: 404, body: { error: 'project not found' } },
    })
    renderApp('/admin/projects/ghost')
    expect(await screen.findByRole('alert')).toHaveTextContent(/project not found/i)
  })

  it('shows linked handoffs and their files without duplicating project history', async () => {
    const handoff = {
      id: '7', project_slug: 'atlas', project_name: 'Atlas', title: 'Continue Atlas', description: 'Release context', scope: 'Release', source: 'Codex',
      created_at: '2026-09-04T08:00:00Z', updated_at: '2026-09-04T09:00:00Z', draft_count: 0, ready_count: 1, in_progress_count: 0, blocked_count: 0, done_count: 0,
    }
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'GET /admin/api/handoffs': { body: { handoffs: [handoff] } },
      'GET /admin/api/projects/atlas/files': { body: { files: [{ id: '15', message_id: '11', handoff_id: '7', handoff_title: 'Continue Atlas', filename: 'checks.txt', media_type: 'text/plain', size_bytes: 42, sha256: 'abc', created_at: '2026-09-04T09:00:00Z' }] } },
    })
    renderApp('/admin/projects/atlas/handoffs')
    const linked = await screen.findByRole('region', { name: /project handoffs/i })
    expect(within(linked).getByRole('link', { name: /continue atlas/i })).toHaveAttribute('href', '/admin/handoffs/7')
    await userEvent.setup().click(screen.getByRole('link', { name: /^files$/i }))
    const files = await screen.findByRole('region', { name: /project files/i })
    expect(within(files).getByRole('link', { name: /checks.txt/i })).toHaveAttribute('href', '/admin/api/handoff-files/15')
    expect(screen.queryByText('checks.txt', { selector: '.timeline *' })).not.toBeInTheDocument()
  })

  it('loads every page of project handoffs', async () => {
    const handoff = {
      id: '7', project_slug: 'atlas', project_name: 'Atlas', title: 'Continue Atlas', description: 'Release context', scope: 'Release', source: 'Codex',
      created_at: '2026-09-04T08:00:00Z', updated_at: '2026-09-04T09:00:00Z', draft_count: 0, ready_count: 1, in_progress_count: 0, blocked_count: 0, done_count: 0,
    }
    const older = { ...handoff, id: '6', title: 'Older Atlas handoff' }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'GET /admin/api/handoffs': [
        { body: { handoffs: [handoff], next_before: 'project-cursor' } },
        { body: { handoffs: [older] } },
      ],
    })
    renderApp('/admin/projects/atlas/handoffs')
    const linked = await screen.findByRole('region', { name: /project handoffs/i })
    await userEvent.setup().click(within(linked).getByRole('button', { name: /load more handoffs/i }))
    expect(await within(linked).findByRole('link', { name: /older atlas handoff/i })).toBeInTheDocument()
    expect(calls.filter((call) => call.path === '/admin/api/handoffs').at(-1)?.url.searchParams.get('before')).toBe('project-cursor')
  })
})
