import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { atlas, authenticatedSession, beacon, mockApi, renderApp, searchResponse } from './helpers'

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
    await screen.findByRole('option', { name: 'Atlas' })
    await user.selectOptions(screen.getByLabelText(/^project$/i), 'atlas')
    await user.selectOptions(screen.getByLabelText(/^kind$/i), 'decision')
    await user.type(input, 'postgres{Enter}')
    const results = await screen.findByRole('list', { name: /results/i })
    const items = within(results).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('Atlas')
    expect(items[0]).toHaveTextContent('entry:41')
    expect(items[0]).toHaveTextContent('decision')
    expect(items[0]).toHaveTextContent('agent-one')
    expect(items[0]).toHaveTextContent('Use PostgreSQL everywhere.')
    expect(within(items[0]!).getByRole('link', { name: /atlas/i })).toHaveAttribute('href', '/admin/projects/atlas')
    expect(items[1]).toHaveTextContent('project:atlas')
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
    expect(await screen.findByRole('list', { name: /results/i })).toBeInTheDocument()
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
})
