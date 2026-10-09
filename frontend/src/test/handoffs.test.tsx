import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { HandoffDetail, HandoffPage } from '../api'
import { atlas, authenticatedSession, mockApi, renderApp } from './helpers'

const detail: HandoffDetail = {
  handoff: {
    id: '7',
    project_slug: 'atlas',
    project_name: 'Atlas',
    title: 'Continue the release',
    description: 'Everything the next agent needs',
    scope: 'Frontend release checks',
    source: 'Codex CLI',
    created_at: '2026-09-04T08:00:00Z',
    updated_at: '2026-09-04T09:00:00Z',
    draft_count: 0,
    ready_count: 1,
    in_progress_count: 0,
    blocked_count: 0,
    done_count: 0,
  },
  messages: [
    {
      id: '11',
      handoff_id: '7',
      body: 'Run the production smoke checks.',
      target: 'Claude',
      delivery_state: 'unseen',
      work_state: 'ready',
      source: 'Codex CLI',
      created_at: '2026-09-04T09:00:00Z',
      status_updated_at: '2026-09-04T09:00:00Z',
      status_updated_source: 'Codex CLI',
      files: [{ id: '15', message_id: '11', filename: 'checks.txt', media_type: 'text/plain', size_bytes: 42, sha256: 'abc', created_at: '2026-09-04T09:00:00Z' }],
    },
  ],
}

const page: HandoffPage = { handoffs: [detail.handoff] }

const research: HandoffDetail = {
  // A research task's description is its objective, and it has no work scope.
  handoff: { ...detail.handoff, id: '9', kind: 'research', research_status: 'review', title: 'Vector DB survey', description: 'Compare vector databases', scope: '', ready_count: 0, blocked_count: 1, done_count: 3 },
  messages: [
    { ...detail.messages[0]!, id: '21', handoff_id: '9', body: '**Research task** · deliverable: report', work_state: 'blocked', target: '', files: [] },
    { ...detail.messages[0]!, id: '22', handoff_id: '9', body: 'Run 1 started by Adastrion dispatcher.', source: 'ledger', work_state: 'done', target: '', files: [] },
    { ...detail.messages[0]!, id: '23', handoff_id: '9', body: '# Findings', source: 'Researcher (run 1)', work_state: 'done', target: '', files: [] },
  ],
  research: {
    message_id: '21', state: 'blocked', phase: 'review', spec: { objective: 'Compare', acceptance: ['Cited'], deliverable: 'report', execution_mode: 'until_done' }, depends_on: [],
    attempt: 1, failures: 0, max_attempts: 3, runner: '', progress: '', last_error: '', checkpoint: '',
  },
}

describe('handoff inbox', () => {
  it('shows a research task for review with Accept and Send back on its status panel', async () => {
    const accepted = { ...research.messages[0]!, work_state: 'done' as const }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: { handoffs: [research.handoff, detail.handoff] } },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/9': { body: research },
      'POST /admin/api/handoff-messages/21/actions': { body: accepted },
    })
    renderApp('/admin/handoffs')
    const list = await screen.findByRole('list', { name: /handoffs/i })
    // The task shows its state, not message counts (its run notes would read as "3 Done"); a general handoff keeps its counts.
    const task = within(list).getByRole('link', { name: /vector db survey/i })
    expect(within(task).getByText('Research')).toBeInTheDocument()
    expect(within(task).getByText('Ready for review')).toHaveAttribute('data-research', 'review')
    expect(within(task).queryByText(/blocked|done/i)).not.toBeInTheDocument()
    expect(within(within(list).getByRole('link', { name: /continue the release/i })).getByText('1 Ready')).toBeInTheDocument()
    await userEvent.setup().click(task)
    const panel = await screen.findByRole('region', { name: /research status/i })
    expect(within(panel).getByRole('heading', { name: 'Ready for review' })).toBeInTheDocument()
    expect(within(panel).getByText(/accept it, or reply with what to change and send back/i)).toBeInTheDocument()
    expect(within(panel).getByRole('button', { name: 'Send back' })).toBeInTheDocument()
    // The brief may be paged out of a long thread, so no message carries task buttons, and claim/block never appear.
    const thread = screen.getByRole('region', { name: /handoff messages/i })
    for (const item of within(thread).getAllByRole('listitem')) expect(within(item).getAllByRole('button').map((button) => button.textContent)).toEqual([' Copy'])
    expect(screen.queryByRole('button', { name: /^(claim|block)$/i })).not.toBeInTheDocument()
    await userEvent.setup().click(within(panel).getByRole('button', { name: 'Accept' }))
    await waitFor(() => expect(calls.find((call) => call.path === '/admin/api/handoff-messages/21/actions')?.body).toMatchObject({ action: 'complete' }))
  })

  it('keeps a research thread to the task: no per-message states, no empty sections, and a reply instead of a target', async () => {
    const reply = { ...research.messages[0]!, id: '24', body: 'Add pgvector numbers', source: 'owner', work_state: 'done' as const }
    const draftReply = { ...research.messages[0]!, id: '25', body: 'Also cover Qdrant', source: 'owner', work_state: 'draft' as const }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: { handoffs: [research.handoff] } },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/9': { body: { ...research, messages: [...research.messages, draftReply] } },
      'POST /admin/api/handoffs/9/messages': { status: 201, body: reply },
    })
    renderApp('/admin/handoffs/9')
    const thread = await screen.findByRole('region', { name: /handoff messages/i })
    const items = within(thread).getAllByRole('listitem')
    expect(items).toHaveLength(4)
    expect(within(thread).queryByText(/^(unseen|seen|ready|blocked|done)$/i)).not.toBeInTheDocument()
    // Only a draft keeps its marker, so its Publish button makes sense.
    expect(within(items[3]!).getByText('Draft')).toBeInTheDocument()
    expect(within(items[3]!).getByRole('button', { name: 'Publish' })).toBeInTheDocument()
    expect(within(thread).getAllByText('Draft')).toHaveLength(1)
    expect(screen.getByText('Description').closest('dl')).toHaveTextContent('Compare vector databases')
    expect(screen.queryByText('Work scope')).not.toBeInTheDocument()
    const form = screen.getByRole('form', { name: 'Reply' })
    expect(within(form).getByRole('heading', { name: 'Reply' })).toBeInTheDocument()
    expect(within(form).queryByLabelText(/target/i)).not.toBeInTheDocument()
    // The reply text leads; Files follows it.
    expect(within(form).getByLabelText('Feedback or answer').compareDocumentPosition(within(form).getByLabelText('Files')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const user = userEvent.setup()
    await user.type(within(form).getByLabelText('Feedback or answer'), 'Add pgvector numbers')
    await user.click(within(form).getByRole('button', { name: 'Add reply' }))
    await waitFor(() => expect(calls.find((call) => call.path === '/admin/api/handoffs/9/messages')?.body).toEqual({ body: 'Add pgvector numbers', target: '', draft: false }))
    expect(await screen.findByRole('status')).toHaveTextContent(/reply added/i)
  })

  it('hides an empty description or work scope on any handoff', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/7': { body: { ...detail, handoff: { ...detail.handoff, description: '  \n' } } },
    })
    renderApp('/admin/handoffs/7')
    expect(await screen.findByText('Work scope')).toBeInTheDocument()
    expect(screen.queryByText('Description')).not.toBeInTheDocument()
    // A general handoff keeps its composer as it was.
    const form = screen.getByRole('form', { name: /append handoff message/i })
    expect(within(form).getByRole('heading', { name: 'Continue this handoff' })).toBeInTheDocument()
    expect(within(form).getByLabelText(/target/i)).toBeInTheDocument()
    expect(within(form).getByLabelText('Files').compareDocumentPosition(within(form).getByLabelText('Message')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('keeps the two create buttons together, beside the title', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
    })
    renderApp('/admin/handoffs')
    const title = await screen.findByRole('heading', { name: 'Handoffs', level: 1 })
    const group = screen.getByRole('link', { name: 'Research' }).parentElement
    expect(screen.getByRole('link', { name: 'New' }).parentElement).toBe(group)
    expect(title.nextElementSibling).toBe(group)
    // Full-size, like the other create buttons.
    for (const name of ['Research', 'New']) expect(screen.getByRole('link', { name })).not.toHaveClass('btn-small')
  })


  it('opens a handoff with separate delivery and work states', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/7': { body: detail },
    })
    renderApp('/admin/handoffs')
    const list = await screen.findByRole('list', { name: /handoffs/i })
    await userEvent.setup().click(within(list).getByRole('link', { name: /continue the release/i }))
    expect(await screen.findByRole('heading', { name: 'Continue the release', level: 1 })).toBeInTheDocument()
    const thread = screen.getByRole('region', { name: /handoff messages/i })
    expect(within(thread).getByText('Unseen')).toBeInTheDocument()
    expect(within(thread).getByText('Ready')).toBeInTheDocument()
    expect(within(thread).getByText('Run the production smoke checks.')).toBeInTheDocument()
    expect(within(thread).getByRole('link', { name: /checks.txt/i })).toHaveAttribute('href', '/admin/api/handoff-files/15')
    expect(screen.getByRole('button', { name: /copy full handoff/i })).toBeInTheDocument()
  })

  it('uploads selected files before publishing a new handoff', async () => {
    const draft = { ...detail, handoff: { ...detail.handoff, id: '8', title: 'New transfer', draft_count: 1, ready_count: 0 }, messages: [{ ...detail.messages[0]!, id: '12', handoff_id: '8', work_state: 'draft' as const, files: [] }] }
    const ready = { ...draft.messages[0]!, work_state: 'ready' as const }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'POST /admin/api/handoffs': { status: 201, body: draft },
      'POST /admin/api/handoff-messages/12/files': { status: 201, body: { id: '16', message_id: '12', filename: 'context.txt', media_type: 'text/plain', size_bytes: 7, sha256: 'def', created_at: '2026-09-04T10:00:00Z' } },
      'POST /admin/api/handoff-messages/12/actions': { body: ready },
      'GET /admin/api/handoffs/8': { body: { ...draft, messages: [ready] } },
    })
    renderApp('/admin/handoffs/new')
    const form = await screen.findByRole('form', { name: /new handoff/i })
    const user = userEvent.setup()
    await user.type(within(form).getByLabelText(/^title$/i), 'New transfer')
    await user.type(within(form).getByLabelText(/description/i), 'Pass the context')
    await user.type(within(form).getByLabelText(/work scope/i), 'Atlas release')
    await user.selectOptions(within(form).getByLabelText(/project/i), 'atlas')
    await user.type(within(form).getByLabelText(/target/i), 'Claude')
    await user.type(within(form).getByLabelText(/message/i), 'Continue here')
    await user.upload(within(form).getByLabelText(/files/i), new File(['context'], 'context.txt', { type: 'text/plain' }))
    await user.click(within(form).getByRole('button', { name: /create handoff/i }))
    await waitFor(() => expect(calls.some((call) => call.path === '/admin/api/handoff-messages/12/actions')).toBe(true))
    expect(calls.find((call) => call.path === '/admin/api/handoffs' && call.method === 'POST')?.body).toMatchObject({ title: 'New transfer', project_slug: 'atlas', draft: true })
    expect(calls.findIndex((call) => call.path.endsWith('/files'))).toBeLessThan(calls.findIndex((call) => call.path.endsWith('/actions')))
    expect(await screen.findByRole('status')).toHaveTextContent(/handoff ready/i)
  })

  it('queues research with files: a draft, the files on its brief, then Queue', async () => {
    const queued = { ...research.messages[0]!, id: '31', handoff_id: '12', work_state: 'ready' as const }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'POST /admin/api/research': { status: 201, body: { handoff_id: '12', message_id: '31', state: 'draft' } },
      'POST /admin/api/handoff-messages/31/files': { status: 201, body: { id: '40', message_id: '31', filename: 'bench.csv', media_type: 'text/csv', size_bytes: 5, sha256: 'abc', created_at: '2026-09-04T10:00:00Z' } },
      'POST /admin/api/handoff-messages/31/actions': { body: queued },
      'GET /admin/api/handoffs/12': { body: { ...research, handoff: { ...research.handoff, id: '12' } } },
    })
    renderApp('/admin/handoffs/new?kind=research')
    const form = await screen.findByRole('form', { name: /new research task/i })
    const user = userEvent.setup()
    await user.type(within(form).getByLabelText(/^title$/i), 'Benchmark engines')
    await user.selectOptions(within(form).getByLabelText(/project/i), 'atlas')
    await user.type(within(form).getByLabelText(/objective/i), 'Rerun the attached benchmark')
    await user.type(within(form).getByLabelText(/checks/i), '  {enter} ')
    await user.click(within(form).getByRole('button', { name: /queue research/i }))
    expect(await within(form).findByRole('alert')).toHaveTextContent(/1 to 20 checks/i)
    await user.clear(within(form).getByLabelText(/checks/i))
    await user.type(within(form).getByLabelText(/checks/i), 'Return a CSV{enter}  {enter}Cite the source')
    await user.selectOptions(within(form).getByLabelText(/deliverable/i), 'dataset')
    await user.upload(within(form).getByLabelText(/files/i), new File(['a,b\n'], 'bench.csv', { type: 'text/csv' }))
    await user.click(within(form).getByRole('button', { name: /queue research/i }))
    await waitFor(() => expect(calls.some((call) => call.path === '/admin/api/handoff-messages/31/actions')).toBe(true))
    expect(calls.find((call) => call.path === '/admin/api/research')?.body).toEqual({ title: 'Benchmark engines', project_slug: 'atlas', objective: 'Rerun the attached benchmark', acceptance: ['Return a CSV', 'Cite the source'], deliverable: 'dataset', draft: true })
    expect(calls.findIndex((call) => call.path.endsWith('/files'))).toBeLessThan(calls.findIndex((call) => call.path.endsWith('/actions')))
    expect(calls.find((call) => call.path.endsWith('/actions'))?.body).toMatchObject({ action: 'publish' })
    expect(await screen.findByRole('status')).toHaveTextContent(/research queued/i)
  })

  it('copies a message as plain text without rendering injected markup', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/7': { body: { ...detail, messages: [{ ...detail.messages[0]!, body: '<script>bad()</script>' }] } },
    })
    renderApp('/admin/handoffs/7')
    const thread = await screen.findByRole('region', { name: /handoff messages/i })
    expect(within(thread).queryByText('bad()', { selector: 'script' })).not.toBeInTheDocument()
    await userEvent.setup().click(within(thread).getByRole('button', { name: /copy message/i }))
    expect(writeText).toHaveBeenCalledWith('<script>bad()</script>')
  })

  it('shows messages as Markdown, safely, with a switch back to plain text', async () => {
    localStorage.removeItem('ledger.markdown-preview')
    const body = '# Plan\n\n- [x] **Contract** done\n\n| Step | Owner |\n|---|---|\n| M0 | codex |\n\n```\nPOST /v1/run\n```\n\n[bad](javascript:alert(1)) <b>raw</b> ![pixel](https://tracker.example/p.png)'
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/7': { body: { ...detail, messages: [{ ...detail.messages[0]!, body }] } },
    })
    renderApp('/admin/handoffs/7')
    const thread = await screen.findByRole('region', { name: /handoff messages/i })
    expect(within(thread).getByRole('heading', { name: 'Plan' })).toBeInTheDocument()
    expect(thread.querySelector('ul.contains-task-list')).not.toBeNull()
    expect(within(thread).getByText('Contract', { selector: 'strong' })).toBeInTheDocument()
    expect(within(thread).getByRole('table')).toHaveTextContent('M0codex')
    expect(within(thread).getByText('POST /v1/run', { selector: 'code' })).toBeInTheDocument()
    // No raw HTML, no script links, no remote image loads.
    expect(thread.querySelector('b')).toBeNull()
    expect(thread.querySelector('img')).toBeNull()
    expect(within(thread).getByRole('link', { name: /pixel \(image link\)/ })).toHaveAttribute('href', 'https://tracker.example/p.png')
    expect(within(thread).getByText('bad').closest('a')?.getAttribute('href') ?? '').not.toMatch(/javascript/i)
    // Off: the text exactly as written, remembered for next time.
    await userEvent.setup().click(within(thread).getByRole('switch', { name: 'Markdown preview' }))
    expect(within(thread).queryByRole('heading', { name: 'Plan' })).not.toBeInTheDocument()
    expect(within(thread).getByText(/# Plan/)).toBeInTheDocument()
    expect(localStorage.getItem('ledger.markdown-preview')).toBe('off')
  })

  it('retries files on a saved draft and publishes only after upload', async () => {
    const draftMessage = { ...detail.messages[0]!, work_state: 'draft' as const, files: [] }
    const uploaded = { id: '16', message_id: '11', filename: 'retry.txt', media_type: 'text/plain', size_bytes: 5, sha256: 'def', created_at: '2026-09-04T10:00:00Z' }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/7': { body: { ...detail, messages: [draftMessage] } },
      'POST /admin/api/handoff-messages/11/files': { status: 201, body: uploaded },
      'POST /admin/api/handoff-messages/11/actions': { body: { ...draftMessage, work_state: 'ready', files: [] } },
    })
    renderApp('/admin/handoffs/7')
    const thread = await screen.findByRole('region', { name: /handoff messages/i })
    const user = userEvent.setup()
    await user.upload(within(thread).getByLabelText(/add files/i), new File(['retry'], 'retry.txt', { type: 'text/plain' }))
    await user.click(within(thread).getByRole('button', { name: /upload and publish/i }))
    await waitFor(() => expect(calls.findIndex((call) => call.path.endsWith('/files'))).toBeLessThan(calls.findIndex((call) => call.path.endsWith('/actions'))))
    expect(await within(thread).findByRole('link', { name: /retry.txt/i })).toHaveAttribute('href', '/admin/api/handoff-files/16')
    expect(within(thread).getByText('Ready')).toBeInTheDocument()
  })

  it('filters the inbox by project and target routing hint', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
    })
    renderApp('/admin/handoffs')
    await screen.findByRole('list', { name: /handoffs/i })
    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText(/filter by project/i), 'atlas')
    await user.type(screen.getByLabelText(/filter by target/i), 'Claude')
    await waitFor(() => {
      const request = calls.filter((call) => call.path === '/admin/api/handoffs').at(-1)
      expect(request?.url.searchParams.get('project')).toBe('atlas')
      expect(request?.url.searchParams.get('target')).toBe('Claude')
    })
  })

  it('loads the next inbox page', async () => {
    const older = { ...detail.handoff, id: '6', title: 'Older handoff' }
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': [
        { body: { handoffs: [detail.handoff], next_before: 'cursor-1' } },
        { body: { handoffs: [older] } },
      ],
      'GET /admin/api/projects': { body: { projects: [atlas] } },
    })
    renderApp('/admin/handoffs')
    await screen.findByRole('link', { name: /continue the release/i })
    await userEvent.setup().click(screen.getByRole('button', { name: /load more handoffs/i }))
    expect(await screen.findByRole('link', { name: /older handoff/i })).toBeInTheDocument()
    expect(calls.filter((call) => call.path === '/admin/api/handoffs').at(-1)?.url.searchParams.get('before')).toBe('cursor-1')
  })

  it('clears the native file picker after appending', async () => {
    const draftMessage = { ...detail.messages[0]!, id: '12', work_state: 'draft' as const, files: [] }
    const uploaded = { id: '16', message_id: '12', handoff_id: '7', filename: 'context.txt', media_type: 'text/plain', size_bytes: 7, sha256: 'def', created_at: '2026-09-04T10:00:00Z' }
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/handoffs': { body: page },
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'GET /admin/api/handoffs/7': { body: detail },
      'POST /admin/api/handoffs/7/messages': { status: 201, body: draftMessage },
      'POST /admin/api/handoff-messages/12/files': { status: 201, body: uploaded },
      'POST /admin/api/handoff-messages/12/actions': { body: { ...draftMessage, work_state: 'ready', files: [uploaded] } },
    })
    renderApp('/admin/handoffs/7')
    const form = await screen.findByRole('form', { name: /append handoff message/i })
    const picker = within(form).getByLabelText(/files/i) as HTMLInputElement
    const user = userEvent.setup()
    await user.upload(picker, new File(['context'], 'context.txt', { type: 'text/plain' }))
    await user.type(within(form).getByLabelText(/message/i), 'More context')
    await user.click(within(form).getByRole('button', { name: /append message/i }))
    await waitFor(() => expect(picker.files).toHaveLength(0))
  })
})
