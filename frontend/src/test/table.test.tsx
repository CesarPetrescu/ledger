import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { ProjectSummaries, TableEntry } from '../api'
import { atlas, authenticatedSession, beacon, decisionEntry, mockApi, noteEntry, renderApp } from './helpers'

const now = new Date().toISOString()
const owner = { read: false, starred: false, handled: false }
const summaries: ProjectSummaries = {
  projects: [
    { slug: 'atlas', name: 'Atlas', tier: 'focus', deadline: 'Friday', needs_me: 'Review the migration', last_entry_at: now, open_todos: 2, week_entries: 5, week_agents: ['claude-code', 'codex'], status_title: 'Deployed table page', status_body: 'long text', status_at: now, status_source: 'codex', digest: 'Shipped the table page; two todos remain.', digest_at: now, status_state: 'in_progress', needs_you: 1 },
    { slug: 'beacon', name: 'Beacon', tier: 'park', deadline: '', needs_me: '', open_todos: 0, week_entries: 0, week_agents: [], status_title: '', status_body: '', status_source: '', digest: '', status_state: '', needs_you: 0 },
  ],
  metadata: { total: 10, ready: 4, failed: 0, active: true },
}
const todo: TableEntry = {
  ...noteEntry, owner, id: '50', kind: 'todo', body: 'We should add CSV export to the table page because phones…', source: 'codex', created_at: now, project_name: 'Atlas',
  meta: { title: 'Add CSV export', tags: ['export'], priority: 'high', refs: ['internal/admin/server.go'], origin: 'model' },
}
const doneTodo: TableEntry = { ...todo, id: '49', meta: { ...todo.meta!, title: 'Fix login', priority: 'low' }, resolved_by: { entry_id: '51', origin: 'model', created_at: now } }
const base = { 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/projects': { body: { projects: [atlas, beacon] } }, 'GET /admin/api/table/projects': { body: summaries } }

describe('table', () => {
  it('opens on a per-project summary with extraction progress', async () => {
    mockApi(base)
    renderApp('/admin/table?view=projects')
    const table = await screen.findByRole('table')
    const atlasRow = within(table).getAllByRole('row')[1]!
    expect(atlasRow).toHaveTextContent('Shipped the table page; two todos remain.')
    expect(atlasRow).toHaveTextContent('Latest: Deployed table page')
    expect(atlasRow).toHaveTextContent('Needs you: Review the migration')
    expect(within(atlasRow).getByRole('link', { name: '2' })).toHaveAttribute('href', '/admin/table?view=todos&project=atlas')
    expect(within(atlasRow).getByText('codex')).toBeInTheDocument()
    expect(within(table).getAllByRole('row')[2]).toHaveTextContent('No status yet')
    expect(screen.getByText(/AI summaries: 4 of 10/)).toHaveAttribute('role', 'status')
    expect(screen.getByRole('link', { name: 'Projects', current: 'page' })).toBeInTheDocument()
    expect(within(atlasRow).getByText('In progress')).toBeInTheDocument()
    expect(within(atlasRow).getByRole('link', { name: '1 thing needs you' })).toHaveAttribute('href', '/admin/table?view=inbox')
  })

  it('opens on the inbox: asks, urgent todos, blocked projects, and digests', async () => {
    const ask: TableEntry = { ...noteEntry, owner, id: '70', created_at: now, project_name: 'Atlas', meta: { title: 'Pricing question', tags: [], refs: [], origin: 'model', ask: 'Confirm the pricing claims', importance: 'important' } }
    const blockedSummary = { ...summaries.projects[1]!, status_state: 'blocked', status_title: 'Waiting on legal' }
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/inbox': { body: { needs_you: [ask], todos: [todo], todos_total: 5, projects: [summaries.projects[0]!, blockedSummary] } },
      'POST /admin/api/entries/70/owner': { body: { ...owner, handled: true } },
    })
    renderApp('/admin/table')
    const asks = await screen.findByRole('region', { name: 'Needs you' })
    expect(screen.getByRole('link', { name: 'Inbox', current: 'page' })).toBeInTheDocument()
    expect(within(asks).getByRole('button', { name: 'Confirm the pricing claims' })).toBeInTheDocument()
    expect(within(asks).getByText('Pricing question')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Todos' })).getByText('1 of 5')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Blocked' })).getByText(/Waiting on legal/)).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'This week' })).getByText('Shipped the table page; two todos remain.')).toBeInTheDocument()
    await userEvent.setup().click(within(asks).getByRole('button', { name: 'Handled' }))
    expect(await screen.findByText('Marked handled.')).toBeInTheDocument()
    expect(calls.find((call) => call.method === 'POST')?.body).toEqual({ handled: true })
  })

  it('shows short facts as badges and in the details instead of the full text', async () => {
    const day = (offset: number) => { const d = new Date(); d.setDate(d.getDate() + offset); return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}` }
    const old = new Date(Date.now() - 20 * 86400000).toISOString()
    const entries: TableEntry[] = [
      { ...todo, id: '81', created_at: old, meta: { ...todo.meta!, title: 'Old overdue task', size: 'L', due: day(-1), importance: 'important', gist: 'Needed before launch' } },
      { ...todo, id: '82', meta: { ...todo.meta!, title: 'Upcoming task', size: 'S', due: day(3) } },
    ]
    mockApi({ ...base, 'GET /admin/api/entries': { body: { entries, sources: [], tags: [] } } })
    renderApp('/admin/table?view=todos')
    const group = await screen.findByRole('region', { name: 'Atlas' })
    const rows = within(group).getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('Important')
    expect(rows[0]).toHaveTextContent('Overdue')
    expect(rows[0]).toHaveTextContent('Stale')
    expect(rows[0]).toHaveTextContent('L')
    expect(rows[0]).toHaveTextContent('Needed before launch')
    expect(rows[0]).not.toHaveTextContent('phones…')
    expect(rows[1]).toHaveTextContent(/Due \d/)
    expect(rows[1]).not.toHaveTextContent('Stale')
  })

  it('hides extraction progress when no extractor is running', async () => {
    mockApi({ ...base, 'GET /admin/api/table/projects': { body: { ...summaries, metadata: { ...summaries.metadata, active: false } } } })
    renderApp('/admin/table?view=projects')
    await screen.findByRole('table')
    expect(screen.queryByText(/AI summaries/)).not.toBeInTheDocument()
  })

  it('lists open todos by priority, marks one done, and filters by tag', async () => {
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/entries': (_init, url) => ({ body: { entries: url.searchParams.get('tag') === 'export' ? [todo] : [{ ...todo, id: '48', meta: { ...todo.meta!, title: 'Tidy docs', priority: 'low', tags: [] } }, todo], sources: ['codex'], tags: ['export'] } }),
      'POST /admin/api/entries/50/resolve': { status: 201, body: { ...noteEntry, owner, kind: 'status' } },
      'GET /admin/api/entries/50/related': { body: { related: [{ ...decisionEntry, owner, id: '7', project_name: 'Beacon', slug: 'beacon', similarity: 0.8, meta: { title: 'Export design decision', tags: [], refs: [], origin: 'model' } }] } },
    })
    renderApp('/admin/table?view=todos&project=atlas')
    const group = await screen.findByRole('region', { name: 'Atlas' })
    const titles = within(group).getAllByRole('button', { name: /add csv export|tidy docs/i }).map((button) => button.textContent)
    expect(titles).toEqual(['Add CSV export', 'Tidy docs'])
    const request = calls.find((call) => call.path === '/admin/api/entries')!
    expect(Object.fromEntries(request.url.searchParams)).toMatchObject({ kind: 'todo', status: 'open', project: 'atlas' })
    expect(within(group).getByText('high')).toBeInTheDocument()

    const user = userEvent.setup()
    await user.click(within(group).getByRole('button', { name: 'Add CSV export' }))
    expect(within(group).getByText(/phones…/)).toBeInTheDocument()
    expect(within(group).getByText('internal/admin/server.go')).toBeInTheDocument()
    expect(within(group).getByText(/generated by AI/)).toBeInTheDocument()
    expect(await within(group).findByRole('link', { name: 'Export design decision' })).toHaveAttribute('href', '/admin/table?view=activity&project=beacon&q=Export%20design%20decision')

    await user.click(within(group).getAllByRole('button', { name: 'Mark done' })[0]!)
    expect(await screen.findByText('Todo marked done.')).toBeInTheDocument()
    expect(calls.find((call) => call.method === 'POST')?.init.headers).toMatchObject({ 'X-CSRF-Token': 'csrf-123' })

    await user.click(within(group).getByRole('button', { name: 'Filter by tag export' }))
    expect(await screen.findByText(/^1 row loaded/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /download csv/i }).getAttribute('href')).toContain('tag=export')
  })

  it('keeps todos of same-named projects apart', async () => {
    const other: TableEntry = { ...todo, id: '60', slug: 'atlas-2', meta: { ...todo.meta!, title: 'Other atlas task' } }
    mockApi({ ...base, 'GET /admin/api/projects': { body: { projects: [atlas, { ...atlas, slug: 'atlas-2' }] } }, 'GET /admin/api/entries': { body: { entries: [todo, other], sources: [], tags: [] } } })
    renderApp('/admin/table?view=todos')
    const groups = await screen.findAllByRole('region', { name: 'Atlas' })
    expect(groups).toHaveLength(2)
    const projectFilter = screen.getByRole('combobox', { name: /filter by project/i })
    expect(within(projectFilter).getAllByRole('option').map((option) => option.textContent)).toEqual(['Any project', 'Atlas (atlas)', 'Atlas (atlas-2)'])
    expect(groups.map((group) => within(group).getByRole('heading').textContent)).toEqual(['Atlas atlas 1', 'Atlas atlas-2 1'])
  })

  it('shows done todos with a reopen action', async () => {
    const { calls } = mockApi({ ...base, 'GET /admin/api/entries': { body: { entries: [doneTodo], sources: [], tags: [] } }, 'POST /admin/api/entries/49/reopen': { status: 201, body: { ...noteEntry, owner, kind: 'status', body: 'Reopened: Fix login' } } })
    renderApp('/admin/table?view=todos')
    await screen.findByText('Fix login')
    await userEvent.setup().selectOptions(screen.getByRole('combobox', { name: /filter by state/i }), 'done')
    expect(screen.getByText(/Done \(detected\)/)).toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Reopen' }))
    expect(await screen.findByText('Todo reopened.')).toBeInTheDocument()
    expect(calls.some((call) => call.path === '/admin/api/entries/49/reopen')).toBe(true)
  })

  it('groups activity by day and collapses runs from one agent', async () => {
    const entries: TableEntry[] = [
      { ...decisionEntry, owner, id: '3', source: 'codex', created_at: now, project_name: 'Atlas', meta: { title: 'Third', tags: [], refs: [], origin: 'model' } },
      { ...decisionEntry, owner, id: '2', source: 'codex', created_at: now, project_name: 'Atlas' },
      { ...decisionEntry, owner, id: '1', source: 'claude-code', created_at: now, project_name: 'Atlas' },
    ]
    mockApi({ ...base, 'GET /admin/api/entries': { body: { entries, sources: [], tags: [] } } })
    renderApp('/admin/table?view=activity')
    const today = await screen.findByRole('region', { name: 'Today' })
    expect(within(today).getByRole('button', { name: 'Third' })).toBeInTheDocument()
    expect(within(today).getAllByRole('button', { name: 'Use PostgreSQL <b>everywhere</b>.' })).toHaveLength(1)
    await userEvent.setup().click(within(today).getByRole('button', { name: '+1 more from codex on Atlas' }))
    expect(within(today).getAllByRole('button', { name: 'Use PostgreSQL <b>everywhere</b>.' })).toHaveLength(2)
    expect(today.querySelector('b')).toBeNull()
  })

  it('adds a row and pages older rows', async () => {
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/entries': (_init, url) => ({ body: url.searchParams.get('before') === '41' ? { entries: [{ ...noteEntry, owner, project_name: 'Atlas' }], sources: [], tags: [] } : { entries: [{ ...decisionEntry, owner, project_name: 'Atlas' }], sources: [], tags: [], next_before: '41' } }),
      'POST /admin/api/projects/beacon/entries': { status: 201, body: { ...noteEntry, owner, slug: 'beacon' } },
    })
    renderApp('/admin/table?view=activity')
    await screen.findByText(/1 row loaded · more available/)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /load more rows/i }))
    expect(await screen.findByText(/2 rows loaded/)).toBeInTheDocument()
    await user.click(screen.getByText('Add a row'))
    const form = screen.getByRole('form', { name: /add row/i })
    await user.selectOptions(within(form).getByRole('combobox', { name: /project/i }), 'beacon')
    await user.type(within(form).getByRole('textbox', { name: /text/i }), 'Call the vendor')
    await user.click(within(form).getByRole('button', { name: /add/i }))
    expect(await screen.findByText('Row added.')).toBeInTheDocument()
    expect(calls.find((call) => call.method === 'POST')?.body).toEqual({ kind: 'note', body: 'Call the vendor' })
  })

  it('folds repeats under the newest copy', async () => {
    const entries: TableEntry[] = [
      { ...decisionEntry, owner, id: '9', kind: 'status', source: 'codex', created_at: now, project_name: 'Atlas', duplicate_of: '2', meta: { title: 'Checkpoint 3', tags: [], refs: [], origin: 'model' } },
      { ...decisionEntry, owner, id: '5', kind: 'status', source: 'claude-code', created_at: now, project_name: 'Atlas', meta: { title: 'Unrelated', tags: [], refs: [], origin: 'model' } },
      { ...decisionEntry, owner, id: '8', kind: 'status', source: 'codex', created_at: now, project_name: 'Atlas', duplicate_of: '2', meta: { title: 'Checkpoint 2', tags: [], refs: [], origin: 'model' } },
      { ...decisionEntry, owner, id: '2', kind: 'status', source: 'codex', created_at: now, project_name: 'Atlas', meta: { title: 'Checkpoint 1', tags: [], refs: [], origin: 'model' } },
      // Both repeat a root older than the loaded page; they still fold together.
      { ...decisionEntry, owner, id: '1', kind: 'status', source: 'codex', created_at: now, project_name: 'Atlas', duplicate_of: '0', meta: { title: 'Old story again', tags: [], refs: [], origin: 'model' } },
      { ...decisionEntry, owner, id: '-1', kind: 'status', source: 'codex', created_at: now, project_name: 'Atlas', duplicate_of: '0', meta: { title: 'Old story once more', tags: [], refs: [], origin: 'model' } },
    ]
    mockApi({ ...base, 'GET /admin/api/entries': { body: { entries, sources: [], tags: [] } } })
    renderApp('/admin/table?view=activity')
    const today = await screen.findByRole('region', { name: 'Today' })
    expect(within(today).getByRole('button', { name: 'Checkpoint 3' })).toBeInTheDocument()
    expect(within(today).queryByRole('button', { name: 'Checkpoint 1' })).not.toBeInTheDocument()
    await userEvent.setup().click(within(today).getByRole('button', { name: '+2 repeats' }))
    const repeats = within(today).getByRole('list', { name: 'Repeats' })
    expect(within(repeats).getAllByRole('listitem').map((item) => item.textContent?.split(' ·')[0])).toEqual(['Checkpoint 2', 'Checkpoint 1'])
    expect(within(today).getByRole('button', { name: 'Unrelated' })).toBeInTheDocument()
    expect(within(today).getByRole('button', { name: 'Old story again' })).toBeInTheDocument()
    expect(within(today).queryByRole('button', { name: 'Old story once more' })).not.toBeInTheDocument()
    expect(within(today).getByRole('button', { name: '+1 repeat' })).toBeInTheDocument()
  })

  it('opens a related entry with its search applied', async () => {
    const { calls } = mockApi({ ...base, 'GET /admin/api/entries': { body: { entries: [{ ...decisionEntry, owner, project_name: 'Beacon', slug: 'beacon' }], sources: [], tags: [] } } })
    renderApp('/admin/table?view=activity&project=beacon&q=Export%20design%20decision')
    expect(await screen.findByRole('searchbox', { name: /search text/i })).toHaveValue('Export design decision')
    expect(Object.fromEntries(calls.find((call) => call.path === '/admin/api/entries')!.url.searchParams)).toMatchObject({ project: 'beacon', q: 'Export design decision' })
  })

  it('hides routine entries in activity unless asked', async () => {
    const { calls } = mockApi({ ...base, 'GET /admin/api/entries': { body: { entries: [], sources: [], tags: [] } } })
    renderApp('/admin/table?view=activity')
    await screen.findByText('No entries match.')
    expect(calls.filter((call) => call.path === '/admin/api/entries').at(-1)?.url.searchParams.get('hide_routine')).toBe('1')
    await userEvent.setup().click(screen.getByRole('checkbox', { name: /show routine entries/i }))
    await screen.findByText('No entries match.')
    expect(calls.filter((call) => call.path === '/admin/api/entries').at(-1)?.url.searchParams.get('hide_routine')).toBeNull()
  })

  it('reads the news feed: why it matters, source, external link, read and star', async () => {
    const news: TableEntry = { ...noteEntry, owner, id: '90', created_at: now, project_name: 'AI news', slug: 'ai-news',
      meta: { title: 'Ollama v0.40 ships MLX by default', tags: [], refs: [], origin: 'model', why: 'Macs get a faster default engine', source: 'GitHub', link: 'https://example.com/release' } }
    const { calls } = mockApi({
      ...base,
      'GET /admin/api/entries': { body: { entries: [news], sources: [], tags: [] } },
      'POST /admin/api/entries/90/owner': { body: { ...owner, read: true } },
    })
    renderApp('/admin/table?view=reading')
    const row = (await screen.findByRole('button', { name: 'Ollama v0.40 ships MLX by default' })).closest('li')!
    expect(calls.find((call) => call.path === '/admin/api/entries')?.url.searchParams.get('reading')).toBe('unread')
    expect(row).toHaveTextContent('Macs get a faster default engine')
    expect(row).toHaveTextContent('GitHub')
    const link = within(row).getByRole('link', { name: /open/i })
    expect(link).toHaveAttribute('href', 'https://example.com/release')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    const user = userEvent.setup()
    await user.click(within(row).getByRole('button', { name: 'Mark read' }))
    expect(await screen.findByText('Marked read.')).toBeInTheDocument()
    expect(calls.find((call) => call.method === 'POST')?.body).toEqual({ read: true })
    await user.click(within(row).getByRole('button', { name: 'Ollama v0.40 ships MLX by default' }))
    expect(within(row).getByText('Why it matters')).toBeInTheDocument()
  })

  it('shows an empty todo state', async () => {
    mockApi({ ...base, 'GET /admin/api/entries': { body: { entries: [], sources: [], tags: [] } } })
    renderApp('/admin/table?view=todos')
    expect(await screen.findByText('No open todos. Nice.')).toBeInTheDocument()
  })
})
