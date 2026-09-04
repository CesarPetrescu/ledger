import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { atlas, atlasDetail, authenticatedSession, beacon, decisionEntry, mockApi, renderApp } from './helpers'

describe('project browser', () => {
  it('lists projects densely, filters by text and tier, and opens the inspector', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas, beacon] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
    })
    renderApp('/admin/projects')
    const list = await screen.findByRole('list', { name: /projects/i })
    expect(within(list).getAllByRole('listitem')).toHaveLength(2)
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
    const meta = screen.getByRole('list', { name: /project metadata/i })
    expect(within(meta).getByText('Goal').nextElementSibling).toHaveTextContent('Ship the operator console')
    expect(within(meta).getByText('Deadline').nextElementSibling).toHaveTextContent('Friday')
    const timeline = screen.getByRole('region', { name: /timeline/i })
    const items = within(timeline).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('Use PostgreSQL <b>everywhere</b>.')
    expect(items[0]).toHaveTextContent('agent-one')
    expect(items[0]?.querySelector('b')).toBeNull()
    expect(within(timeline).queryByRole('button', { name: /delete|edit entry/i })).not.toBeInTheDocument()
  })

  it('appends an entry with the selected kind and prepends it to the timeline', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'POST /admin/api/projects/atlas/entries': { status: 201, body: { id: '42', slug: 'atlas', kind: 'todo', body: 'Write the runbook', source: 'ledger-admin', client_id: 'admin-session-0123456789ab', created_at: '2026-09-04T08:00:00Z' } },
    })
    renderApp('/admin/projects/atlas')
    await screen.findByRole('heading', { name: 'Atlas', level: 1 })
    const user = userEvent.setup()
    const composer = screen.getByRole('form', { name: /append entry/i })
    expect(within(composer).getByRole('button', { name: /^append$/i })).toBeDisabled()
    await user.selectOptions(within(composer).getByLabelText(/kind/i), 'todo')
    await user.type(within(composer).getByLabelText(/body/i), 'Write the runbook')
    await user.click(within(composer).getByRole('button', { name: /^append$/i }))
    expect(await screen.findByRole('status')).toHaveTextContent(/entry appended/i)
    const post = calls.find((call) => call.method === 'POST')
    expect(post?.body).toEqual({ kind: 'todo', body: 'Write the runbook' })
    const timeline = screen.getByRole('region', { name: /timeline/i })
    await waitFor(() => expect(within(timeline).getAllByRole('listitem')).toHaveLength(3))
    expect(within(timeline).getAllByRole('listitem')[0]).toHaveTextContent('Write the runbook')
    expect(within(composer).getByLabelText(/body/i)).toHaveValue('')
    expect(within(screen.getByRole('list', { name: /projects/i })).getByRole('link', { name: /atlas/i }).querySelector('time')).toHaveAttribute('datetime', '2026-09-04T08:00:00Z')
  })

  it('does not duplicate an entry already loaded by a live refresh', async () => {
    const appended = { ...decisionEntry, id: '42', body: 'Already refreshed.' }
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: { ...atlasDetail, entries: [appended, ...atlasDetail.entries] } },
      'POST /admin/api/projects/atlas/entries': { status: 201, body: appended },
    })
    renderApp('/admin/projects/atlas')
    const composer = await screen.findByRole('form', { name: /append entry/i })
    const user = userEvent.setup()
    await user.type(within(composer).getByLabelText(/body/i), appended.body)
    await user.click(within(composer).getByRole('button', { name: /^append$/i }))

    const timeline = screen.getByRole('region', { name: /timeline/i })
    await waitFor(() => expect(within(timeline).getAllByText(appended.body)).toHaveLength(1))
  })

  it('loads older timeline entries without hiding append-only history', async () => {
    const newest = atlasDetail.entries[0]!
    const middle = atlasDetail.entries[1]!
    const older = { ...middle, id: '39', body: 'Oldest retained decision.' }
    const cursor = '9007199254740993'
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': [
        { body: { project: atlas, entries: [newest], next_before: cursor } },
        { body: { project: atlas, entries: [middle, older] } },
      ],
    })
    renderApp('/admin/projects/atlas')
    const timeline = await screen.findByRole('region', { name: /timeline/i })
    expect(within(timeline).getAllByRole('listitem')).toHaveLength(1)

    await userEvent.setup().click(within(timeline).getByRole('button', { name: /load older entries/i }))

    await waitFor(() => expect(within(timeline).getAllByRole('listitem')).toHaveLength(3))
    expect(within(timeline).getByText('Oldest retained decision.')).toBeInTheDocument()
    expect(within(timeline).queryByRole('button', { name: /load older entries/i })).not.toBeInTheDocument()
    const pageCall = calls.filter((call) => call.path === '/admin/api/projects/atlas').at(-1)
    expect(pageCall?.url.searchParams.get('entries')).toBe('200')
    expect(pageCall?.url.searchParams.get('before')).toBe(cursor)
  })

  it('creates a project through the form and shows server validation errors', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
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
    expect(screen.getByText(/no entries yet/i)).toBeInTheDocument()
  })

  it('edits an existing project in place', async () => {
    const savedAtlas = Object.fromEntries(Object.entries({ ...atlas, goal: 'Ship v2' }).filter(([key]) => key !== 'last_entry_at'))
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/atlas': { body: atlasDetail },
      'PUT /admin/api/projects/atlas': { body: savedAtlas },
    })
    renderApp('/admin/projects/atlas')
    await screen.findByRole('heading', { name: 'Atlas', level: 1 })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /edit project/i }))
    const form = screen.getByRole('form', { name: /edit project/i })
    expect(within(form).queryByLabelText(/^slug/i)).not.toBeInTheDocument()
    await user.clear(within(form).getByLabelText(/^goal/i))
    await user.type(within(form).getByLabelText(/^goal/i), 'Ship v2')
    await user.click(within(form).getByRole('button', { name: /save changes/i }))
    expect(await screen.findByRole('status')).toHaveTextContent(/project saved/i)
    const meta = screen.getByRole('list', { name: /project metadata/i })
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
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [newProject] } },
      'GET /admin/api/projects/new': { body: { project: newProject, entries: [] } },
    })
    renderApp('/admin/projects/new')
    expect(await screen.findByRole('heading', { name: 'New Project', level: 1 })).toBeInTheDocument()
    expect(screen.queryByRole('form', { name: /new project/i })).not.toBeInTheDocument()
  })

  it('shows a not-found state for unknown projects', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/projects/ghost': { status: 404, body: { error: 'project not found' } },
    })
    renderApp('/admin/projects/ghost')
    expect(await screen.findByRole('alert')).toHaveTextContent(/project not found/i)
  })
})
