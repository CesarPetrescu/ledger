import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { AgentSummary } from '../api'
import { authenticatedSession, mockApi, noteEntry, renderApp } from './helpers'

const now = new Date().toISOString()
const codex: AgentSummary = {
  name: 'codex', last_active: now, week_entries: 5, entries: 40, projects: [{ slug: 'atlas', name: 'Atlas' }, { slug: 'beacon', name: 'Beacon' }], open_asks: 1, handoffs: 2,
  latest: [{ ...noteEntry, id: '9', source: 'codex', project_name: 'Atlas', owner: { read: false, starred: false, handled: false }, meta: { title: 'Shipped CSV export', tags: [], refs: [], origin: 'model' } }],
}
const quiet: AgentSummary = { name: 'claude-code', week_entries: 0, entries: 3, projects: [], open_asks: 0, handoffs: 0, latest: [] }

describe('agents', () => {
  it('shows what each agent did lately and what it waits on you for', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession,
      'GET /admin/api/agents': { body: { agents: [codex, quiet] } },
    })
    renderApp('/admin/agents')
    const cards = within(await screen.findByRole('list', { name: 'Agents' })).getAllByRole('listitem').filter((item) => item.classList.contains('agent-card'))
    expect(cards).toHaveLength(2)
    expect(cards[0]).toHaveTextContent('5 entries this week · 40 in total')
    expect(within(cards[0]!).getByRole('link', { name: 'Beacon' })).toHaveAttribute('href', '/admin/projects/beacon')
    expect(within(cards[0]!).getByRole('link', { name: /1 question waits for your answer/i })).toHaveAttribute('href', '/admin/')
    expect(within(cards[0]!).getByRole('link', { name: /working on 2 handoffs/i })).toHaveAttribute('href', '/admin/handoffs')
    expect(within(cards[0]!).getByRole('link', { name: 'Shipped CSV export' })).toHaveAttribute('href', '/admin/entries/9')
    expect(within(cards[0]!).getByRole('link', { name: 'All of its activity' })).toHaveAttribute('href', '/admin/table?view=activity&source=codex')
    expect(cards[1]).toHaveTextContent('Quiet this week · 3 in total')
    expect(screen.getByRole('link', { name: 'Agents', current: 'page' })).toBeInTheDocument()
    // Connecting and access settings live on their own page; Agents only links there.
    expect(screen.getByRole('link', { name: 'Manage access' })).toHaveAttribute('href', '/admin/access')
    for (const section of ['Connect an agent', 'Connected apps and access', 'API keys', 'GitHub sync', 'Approval password']) {
      expect(screen.queryByRole('region', { name: section })).not.toBeInTheDocument()
    }
    expect(calls.map((call) => call.path)).toEqual(['/admin/api/session', '/admin/api/agents'])
  })

  it('sends you to Access to connect the first agent', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/agents': { body: { agents: [] } } })
    renderApp('/admin/agents')
    expect(await screen.findByText(/no agent has written to ledger yet/i)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Connect one on Access' })).toHaveAttribute('href', '/admin/access')
  })
})

describe('help', () => {
  it('explains where to look, how the inbox decides, and every label', async () => {
    mockApi({ 'GET /admin/api/session': authenticatedSession })
    renderApp('/admin/help')
    expect(await screen.findByRole('heading', { name: 'Help', level: 1 })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'How the Inbox decides' })).toHaveTextContent(/until you mark it handled/)
    for (const label of ['Asks you', 'Important', 'Stale', 'Check']) expect(screen.getByText(label)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /help: how ledger works/i })).toHaveAttribute('aria-current', 'page')
  })
})
