import { render } from '@testing-library/react'
import { vi } from 'vitest'
import App from '../App'
import type { Client, Entry, Overview, Project, ProjectDetail, RecentEntry, SearchResponse } from '../api'

export type Reply = { status?: number; body?: unknown } | ((init: RequestInit, url: URL) => { status?: number; body?: unknown })

/**
 * Installs a fetch stub keyed by "METHOD /admin/api/path". Query strings are ignored
 * for matching but available to reply functions. Every call is recorded.
 */
export function mockApi(routes: Record<string, Reply | Reply[]>) {
  const calls: { method: string; path: string; url: URL; init: RequestInit; body: unknown }[] = []
  const queues = new Map<string, Reply[]>()
  for (const [key, value] of Object.entries(routes)) {
    queues.set(key, Array.isArray(value) ? [...value] : [value])
  }
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'https://ledger.example.com')
    const method = (init.method ?? 'GET').toUpperCase()
    const key = `${method} ${url.pathname}`
    const body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined
    calls.push({ method, path: url.pathname, url, init, body })
    const queue = queues.get(key)
    if (!queue || queue.length === 0) {
      throw new Error(`unexpected request ${key}`)
    }
    const reply = queue.length > 1 ? queue.shift()! : queue[0]!
    const resolved = typeof reply === 'function' ? reply(init, url) : reply
    const status = resolved.status ?? 200
    if (status === 204) {
      return new Response(null, { status })
    }
    return new Response(JSON.stringify(resolved.body ?? {}), { status, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetchMock)
  return { calls, fetchMock }
}

export function renderApp(path = '/admin/') {
  window.history.replaceState(null, '', path)
  return render(<App />)
}

export function futureSessionExpiry() {
  return new Date(Date.now() + 60 * 60 * 1000).toISOString()
}

export const authenticatedSession = { status: 200, body: { authenticated: true, csrf_token: 'csrf-123', expires_at: futureSessionExpiry() } }
export const anonymousSession = { status: 401, body: { error: 'unauthenticated' } }

export const atlas: Project = {
  slug: 'atlas',
  name: 'Atlas',
  tier: 'focus',
  hours_wk: 8,
  type: 'service',
  description: 'Project memory service',
  goal: 'Ship the operator console',
  deadline: 'Friday',
  needs_me: 'Review the migration',
  automate: 'Nightly reindex',
  stack: 'Go, PostgreSQL',
  updated_at: '2026-09-03T10:00:00Z',
  last_entry_at: '2026-09-03T12:00:00Z',
}

export const beacon: Project = {
  slug: 'beacon',
  name: 'Beacon',
  tier: 'park',
  hours_wk: 0,
  type: '',
  description: '',
  goal: 'Keep the lights on',
  deadline: '',
  needs_me: '',
  automate: '',
  stack: '',
  updated_at: '2026-08-01T10:00:00Z',
}

export const decisionEntry: Entry = {
  id: 41,
  slug: 'atlas',
  kind: 'decision',
  body: 'Use PostgreSQL <b>everywhere</b>.',
  source: 'agent-one',
  client_id: 'client-1',
  created_at: '2026-09-03T12:00:00Z',
}

export const noteEntry: Entry = {
  id: 40,
  slug: 'atlas',
  kind: 'note',
  body: 'Kickoff done.',
  source: 'ledger-admin',
  client_id: 'admin-session-0123456789ab',
  created_at: '2026-09-02T09:30:00Z',
}

export const recent: RecentEntry[] = [
  { ...decisionEntry, project_name: 'Atlas' },
  { ...noteEntry, project_name: 'Atlas' },
]

export const overview: Overview = {
  counts: { projects: 2, entries: 2, oauth_clients: 1, active_access_tokens: 3, active_admin_sessions: 1 },
  projects: [atlas, beacon],
  recent_entries: recent,
}

export const atlasDetail: ProjectDetail = { project: atlas, entries: [decisionEntry, noteEntry] }

export const clients: Client[] = [
  {
    client_id: 'dcr-client-abc',
    kind: 'dcr',
    client_name: 'Agent One',
    redirect_uris: ['http://127.0.0.1:4567/callback'],
    created_at: '2026-08-20T10:00:00Z',
    last_used_at: '2026-09-03T11:00:00Z',
    active_access_tokens: 3,
  },
  {
    client_id: 'https://app.example/client.json',
    kind: 'cimd',
    client_name: 'Desk app',
    redirect_uris: ['https://app.example/callback'],
    created_at: '2026-08-01T10:00:00Z',
    last_used_at: '2026-08-01T10:00:00Z',
    active_access_tokens: 0,
  },
]

export const searchResponse: SearchResponse = {
  hits: [
    {
      ref: 'entry:41',
      kind: 'decision',
      score: 0.91,
      snippet: 'Use PostgreSQL everywhere.',
      project_slug: 'atlas',
      project_name: 'Atlas',
      entry_id: 41,
      created_at: '2026-09-03T12:00:00Z',
      source: 'agent-one',
      client_id: 'client-1',
    },
    {
      ref: 'project:atlas',
      kind: 'project',
      score: 0.42,
      snippet: 'Atlas project memory service',
      project_slug: 'atlas',
      project_name: 'Atlas',
      created_at: '2026-09-03T10:00:00Z',
    },
  ],
  degraded: [],
}
