import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { atlas, authenticatedSession, beacon, mockApi, renderApp, searchResponse } from './helpers'
import { navigate } from '../router'

describe('search', () => {
  it('runs a query with filters and renders ranked results with provenance', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas, beacon] } },
      'POST /admin/api/search': { body: searchResponse },
    })
    renderApp('/admin/search')
    const user = userEvent.setup()
    const input = await screen.findByLabelText(/search memory/i)
    await user.click(screen.getByRole('button', { name: /filters/i }))
    await screen.findByRole('option', { name: 'Atlas' })
    await user.selectOptions(screen.getByLabelText(/^project$/i), 'atlas')
    await user.selectOptions(screen.getByLabelText(/^kind$/i), 'decision')
    await user.type(input, 'postgres{Enter}')
    const results = await screen.findByRole('region', { name: /results/i })
    const items = within(results).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('Atlas')
    expect(items[0]).toHaveTextContent('decision')
    expect(items[0]).toHaveTextContent('Use PostgreSQL everywhere.')
    expect(items[0]?.querySelector('mark')).toHaveTextContent(/postgres/i)
    expect(within(items[0]!).getByRole('link', { name: /postgresql/i })).toHaveAttribute('href', '/admin/projects/atlas')
    expect(items[1]).toHaveTextContent('Atlas')
    const post = calls.find((call) => call.method === 'POST')
    expect(post?.body).toEqual({ q: 'postgres', limit: 20, project: 'atlas', kind: 'decision' })
    expect(window.location.search).toContain('q=postgres')
    expect(screen.queryByRole('status', { name: /degraded/i })).not.toBeInTheDocument()
  })

  it('explains degraded retrieval, empty results, and unavailability with retry', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
      'POST /admin/api/search': [
        { body: { hits: [], degraded: ['vector', 'rerank'] } },
        { status: 503, body: { error: 'search unavailable' } },
        { body: searchResponse },
      ],
    })
    renderApp('/admin/search?q=nothing')
    expect(await screen.findByText(/no results for/i)).toBeInTheDocument()
    const degraded = screen.getByRole('status', { name: /degraded/i })
    expect(degraded).toHaveTextContent(/vector retrieval/i)
    expect(degraded).toHaveTextContent(/reranking/i)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /^search$/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/search is unavailable/i)
    await user.click(screen.getByRole('button', { name: /retry/i }))
    expect(await screen.findByRole('region', { name: /results/i })).toBeInTheDocument()
  })

  it('requires a query before searching', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas] } },
    })
    renderApp('/admin/search')
    const user = userEvent.setup()
    await screen.findByLabelText(/search memory/i)
    await user.click(screen.getByRole('button', { name: /^search$/i }))
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0)
    expect(screen.getByText(/type a query/i)).toBeInTheDocument()
  })

  it('synchronizes the search draft when history navigation changes the query', async () => {
    mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas, beacon] } },
      'POST /admin/api/search': [{ body: searchResponse }, { body: { hits: [], degraded: [] } }, { body: searchResponse }],
    })
    renderApp('/admin/search?q=postgres&project=atlas&kind=decision')
    expect(await screen.findByRole('region', { name: /results/i })).toBeInTheDocument()

    const user = userEvent.setup()
    const input = screen.getByLabelText(/search memory/i)
    await user.clear(input)
    await user.type(input, 'beacon')
    await user.selectOptions(screen.getByLabelText(/^project$/i), 'beacon')
    await user.selectOptions(screen.getByLabelText(/^kind$/i), 'note')
    await user.click(screen.getByRole('button', { name: /^search$/i }))
    expect(await screen.findByText(/no results for “beacon”/i)).toBeInTheDocument()

    navigate('/search?q=postgres&project=atlas&kind=decision')

    await waitFor(() => expect(screen.getByLabelText(/search memory/i)).toHaveValue('postgres'))
    expect(await screen.findByRole('region', { name: /results/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/^project$/i)).toHaveValue('atlas')
    expect(screen.getByLabelText(/^kind$/i)).toHaveValue('decision')
  })

  it('switches between all, project, and entry result scopes', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/projects': { body: { projects: [atlas, beacon] } },
      'POST /admin/api/search': [{ body: searchResponse }, { body: searchResponse }, { body: searchResponse }],
    })
    renderApp('/admin/search?q=postgres')
    await screen.findByRole('region', { name: /results/i })
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: 'Projects' }))
    expect(await screen.findByRole('button', { name: 'Projects' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: 'Entries' }))
    expect(await screen.findByRole('button', { name: 'Entries' })).toHaveAttribute('aria-pressed', 'true')

    expect(calls.filter((call) => call.method === 'POST').map((call) => call.body)).toEqual([
      { q: 'postgres', limit: 20 },
      { q: 'postgres', limit: 20, kind: 'project' },
      { q: 'postgres', limit: 20, kind: 'entry' },
    ])
  })
})
