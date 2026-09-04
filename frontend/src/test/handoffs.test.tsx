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

describe('handoff inbox', () => {
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
