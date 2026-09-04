// Same-origin JSON client for /admin/api. The session cookie is HttpOnly and sent
// by the browser; the CSRF token lives only in module memory for the page lifetime.

export type Tier = 'focus' | 'maintain' | 'park'
export type EntryKind = 'decision' | 'note' | 'todo' | 'status'

export const TIERS: readonly Tier[] = ['focus', 'maintain', 'park']
export const ENTRY_KINDS: readonly EntryKind[] = ['decision', 'note', 'todo', 'status']

export interface Project {
  slug: string
  name: string
  tier: Tier
  hours_wk: number
  type: string
  description: string
  goal: string
  deadline: string
  needs_me: string
  automate: string
  stack: string
  updated_at: string
  last_entry_at?: string
}

export interface ProjectInput {
  name: string
  tier: string
  hours_wk: number
  type?: string
  description?: string
  goal?: string
  deadline?: string
  needs_me?: string
  automate?: string
  stack?: string
}

export interface Entry {
  id: number
  slug: string
  kind: EntryKind
  body: string
  source: string
  client_id: string
  created_at: string
}

export interface RecentEntry extends Entry {
  project_name: string
}

export interface Counts {
  projects: number
  entries: number
  oauth_clients: number
  active_access_tokens: number
  active_admin_sessions: number
}

export interface Overview {
  counts: Counts
  projects: Project[]
  recent_entries: RecentEntry[]
}

export interface ProjectDetail {
  project: Project
  entries: Entry[]
}

export interface SearchHit {
  ref: string
  kind: string
  score: number
  snippet: string
  project_slug: string
  project_name: string
  entry_id?: number
  created_at?: string
  source?: string
  client_id?: string
}

export interface SearchResponse {
  hits: SearchHit[]
  degraded: string[]
}

export interface SearchRequest {
  q: string
  limit: number
  project?: string
  kind?: string
}

export interface Client {
  client_id: string
  kind: 'dcr' | 'cimd'
  client_name: string
  redirect_uris: string[]
  created_at: string
  last_used_at: string
  active_access_tokens: number
}

export interface ClientPage {
  clients: Client[]
  next_offset?: number
}

export interface Session {
  csrf_token: string
  expires_at: string
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export class UnauthorizedError extends ApiError {
  constructor() {
    super(401, 'Not signed in.')
    this.name = 'UnauthorizedError'
  }
}

const GENERIC_FAILURE = 'The server could not complete the request.'

let csrfToken: string | null = null
const unauthorizedListeners = new Set<() => void>()

export function setCsrfToken(token: string | null): void {
  csrfToken = token
}

export function onUnauthorized(listener: () => void): () => void {
  unauthorizedListeners.add(listener)
  return () => unauthorizedListeners.delete(listener)
}

async function request<T>(method: 'GET' | 'POST' | 'PUT', path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  if (method !== 'GET') {
    headers['X-CSRF-Token'] = csrfToken ?? ''
  }
  const init: RequestInit = { method, headers, credentials: 'same-origin', cache: 'no-store' }
  if (body !== undefined) {
    init.body = JSON.stringify(body)
  }
  let response: Response
  try {
    response = await fetch(`/admin/api${path}`, init)
  } catch {
    throw new ApiError(0, 'Network error. Check the connection and try again.')
  }
  if (response.status === 401) {
    for (const listener of unauthorizedListeners) listener()
    throw new UnauthorizedError()
  }
  if (response.status === 204) {
    return undefined as T
  }
  const data: unknown = await response.json().catch(() => null)
  if (!response.ok) {
    const serverMessage = data !== null && typeof data === 'object' && 'error' in data && typeof data.error === 'string' ? data.error : ''
    throw new ApiError(response.status, response.status < 500 && serverMessage ? serverMessage : GENERIC_FAILURE)
  }
  return data as T
}

export const api = {
  getSession: () => request<Session & { authenticated: boolean }>('GET', '/session'),
  login: (password: string) => request<Session>('POST', '/login', { password }),
  logout: () => request<void>('POST', '/logout'),
  getOverview: () => request<Overview>('GET', '/overview'),
  listProjects: (tier?: Tier) => request<{ projects: Project[] }>('GET', tier ? `/projects?tier=${encodeURIComponent(tier)}` : '/projects').then((r) => r.projects),
  getProject: (slug: string) => request<ProjectDetail>('GET', `/projects/${encodeURIComponent(slug)}?entries=200`),
  saveProject: (slug: string, input: ProjectInput) => request<Project>('PUT', `/projects/${encodeURIComponent(slug)}`, input),
  appendEntry: (slug: string, kind: string, body: string) => request<Entry>('POST', `/projects/${encodeURIComponent(slug)}/entries`, { kind, body }),
  search: (input: SearchRequest) => request<SearchResponse>('POST', '/search', input),
  listClients: (offset = 0) => request<ClientPage>('GET', `/oauth/clients?limit=50&offset=${offset}`),
  revokeClient: (clientId: string) => request<{ revoked: number }>('POST', '/oauth/revoke', { client_id: clientId }),
}

export function describeError(error: unknown): string {
  if (error instanceof ApiError) return error.message
  return 'Something went wrong.'
}
