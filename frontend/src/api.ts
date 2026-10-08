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
  research_visible: boolean
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
  id: string
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

export type Priority = 'low' | 'normal' | 'high'

/** Derived by the LLM extractor ('model') or set by console actions ('owner'). */
export type Importance = 'routine' | 'useful' | 'important'
export type StatusState = 'done' | 'in_progress' | 'blocked'

export interface EntryMeta {
  title: string
  tags: string[]
  priority?: Priority
  refs: string[]
  origin: 'model' | 'owner'
  /** One sentence with the key fact the title does not say. */
  gist?: string
  importance?: Importance
  /** What the entry needs from the owner, as a short imperative. */
  ask?: string
  state?: StatusState
  next_step?: string
  blocker?: string
  /** Why it matters (notes) or why it was decided (decisions). */
  why?: string
  size?: 'S' | 'M' | 'L'
  /** YYYY-MM-DD */
  due?: string
  source?: string
  link?: string
  /** A per-project grouping such as "billing". */
  category?: string
  details?: MetaDetails
  /** Fields the model was not sure of. */
  unsure?: LabelField[]
  /** Fields the owner corrected; extraction never overwrites them. */
  edited?: LabelField[]
}

/** Structured extras found in an entry's text. */
export interface MetaDetails {
  checklist?: { text: string; done: boolean }[]
  numbers?: { label: string; value: string }[]
  entities?: string[]
  links?: string[]
  /** Decisions: the option chosen and the alternatives turned down. */
  chosen?: string
  rejected?: string
}

export type LabelField = 'title' | 'gist' | 'tags' | 'priority' | 'importance' | 'ask' | 'state' | 'next_step' | 'blocker' | 'why' | 'size' | 'due' | 'category'
export type LabelPatch = Partial<Record<Exclude<LabelField, 'tags'>, string> & { tags: string[] }>

/** The owner's own triage of an entry. */
export interface OwnerState {
  read: boolean
  starred: boolean
  handled: boolean
  snoozed_until?: string
}

export interface OwnerPatch {
  read?: boolean
  starred?: boolean
  handled?: boolean
  snooze_days?: number
}

export interface TableEntry extends RecentEntry {
  meta?: EntryMeta
  resolved_by?: { entry_id: string; origin: 'model' | 'owner'; created_at: string }
  /** Set when this entry repeats an earlier one in the same project. */
  duplicate_of?: string
  owner: OwnerState
  /** The entry this one answers, and how many answer this one. */
  reply_to?: string
  replies?: number
  /** Where the agent said it wrote from (repo, branch, session, link). */
  context?: string
}

export type HistoryKind = 'created' | 'repeat' | 'resolved' | 'action' | 'labels' | 'reply'

export interface HistoryEvent {
  at: string
  kind: HistoryKind
  actor: string
  text: string
  entry_id?: string
  undone?: boolean
}

/** Who wrote something from the console or the phone. */
export const OWNER_SOURCE = 'ledger-admin'

export interface RelatedEntry extends TableEntry {
  similarity: number
}

export interface EntryFilter {
  project?: string
  kind?: string
  source?: string
  tag?: string
  status?: string
  q?: string
  /** "1" hides entries rated routine. */
  hide_routine?: string
  /** "you" keeps entries asking something of the owner, not yet handled. */
  needs?: string
  /** Linked (news-shaped) entries: "all", "unread", or "starred". */
  reading?: string
  state?: string
  /** YYYY-MM-DD bounds on the due date: from (inclusive), before (exclusive). */
  due_from?: string
  due_before?: string
  /** YYYY-MM-DD bounds on the day a snooze ends: from (inclusive), before (exclusive). */
  wakes_from?: string
  wakes_before?: string
}

export interface EntryTablePage {
  entries: TableEntry[]
  sources: string[]
  tags: string[]
  next_before?: string
}

export interface ProjectSummary {
  slug: string
  name: string
  tier: Tier
  deadline: string
  needs_me: string
  last_entry_at?: string
  open_todos: number
  week_entries: number
  week_agents: string[]
  status_title: string
  status_body: string
  status_at?: string
  status_source: string
  digest: string
  digest_at?: string
  /** The latest status entry's state: done, in_progress, blocked, or "". */
  status_state: string
  /** The blocker (or title) of the status that set status_state. */
  status_detail: string
  needs_you: number
}

/** A quick action the owner took, undoable on its own for a week. */
export interface OwnerAction {
  id: string
  kind: 'resolve' | 'reopen' | 'owner' | 'trash'
  label: string
  project_slug: string
  created_at: string
  undone_at?: string
  undoable: boolean
}

export interface TrashItem {
  id: string
  kind: 'entry' | 'project'
  label: string
  project_slug: string
  entry_count: number
  deleted_at: string
  purge_at: string
}

export interface DeletionPreview {
  name: string
  entries: number
  handoffs: number
  files: number
}

/** Responses of undoable actions carry the action to undo. */
export interface Undoable {
  action_id: string
}

export interface InboxResponse {
  needs_you: TableEntry[]
  todos: TableEntry[]
  todos_total: number
  projects: ProjectSummary[]
}

export interface ProjectSummaries {
  projects: ProjectSummary[]
  /**
   * active is false when no extractor has checked in recently; configured is
   * false when none ever ran; problem says what stops a running one.
   */
  metadata: { total: number; ready: number; failed: number; active: boolean; configured?: boolean; problem?: string }
}

/** One agent's recent work, for the Agents page. */
export interface AgentSummary {
  name: string
  last_active?: string
  week_entries: number
  entries: number
  /** Projects it wrote to this week. */
  projects: { slug: string; name: string }[]
  /** Questions it asked you that are still open. */
  open_asks: number
  /** Handoff messages it claimed and has not finished. */
  handoffs: number
  latest: TableEntry[]
}

function entryQuery(filter: EntryFilter, extra: Record<string, string> = {}): string {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries({ ...filter, ...extra })) if (value) query.set(key, value)
  return query.size ? `?${query}` : ''
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
  next_before?: string
}

export interface SearchHit {
  ref: string
  kind: string
  score: number
  snippet: string
  project_slug: string
  project_name: string
  entry_id?: string
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
  kind: 'dcr' | 'cimd' | 'device'
  client_name: string
  redirect_uris: string[]
  created_at: string
  last_used_at: string
  active_access_tokens: number
  active_refresh_tokens: number
}

export interface ApiKey {
  id: number
  name: string
  prefix: string
  scopes: string[]
  created_at: string
  last_used_at?: string
  revoked_at?: string
}

export interface DeviceRequest {
  user_code: string
  client_name: string
  scope: string
  created_at: string
  expires_at: string
}

export interface ClientPage {
  clients: Client[]
  next_offset?: number
}

export interface CalendarConnection {
  connected: boolean
  server_url?: string
  username?: string
  selected_calendars: number
  connected_at?: string
}

export interface CalendarSource {
  id: string
  name: string
  description?: string
  selected: boolean
}

export interface CalendarEvent {
  id: string
  calendar_id: string
  calendar_name: string
  title: string
  start: string
  end: string
  all_day: boolean
  location?: string
  description?: string
  etag: string
  recurring: boolean
}

export interface CalendarEventInput {
  title: string
  start: string
  end: string
  all_day: boolean
  location?: string
  description?: string
}

export type HandoffWorkState = 'draft' | 'ready' | 'in_progress' | 'blocked' | 'done'
export type HandoffDeliveryState = 'unseen' | 'seen'

export interface Handoff {
  id: string
  project_slug: string
  project_name: string
  title: string
  description: string
  scope: string
  source: string
  client_id?: string
  created_at: string
  updated_at: string
  archived_at?: string
  kind?: 'general' | 'research'
  draft_count: number
  ready_count: number
  in_progress_count: number
  blocked_count: number
  done_count: number
}

export interface HandoffFile {
  id: string
  message_id: string
  handoff_id?: string
  handoff_title?: string
  filename: string
  media_type: string
  size_bytes: number
  sha256: string
  created_at: string
}

export interface HandoffMessage {
  id: string
  handoff_id: string
  body: string
  target: string
  delivery_state: HandoffDeliveryState
  work_state: HandoffWorkState
  source: string
  client_id?: string
  seen_at?: string
  seen_source?: string
  seen_client_id?: string
  claimed_at?: string
  claimed_source?: string
  claimed_client_id?: string
  status_updated_at: string
  status_updated_source: string
  status_updated_client_id?: string
  created_at: string
  files: HandoffFile[]
}

export interface ResearchStatus {
  message_id: string
  state: HandoffWorkState
  phase: '' | 'question' | 'review' | 'dead'
  spec: { objective: string; acceptance: string[]; deliverable: string; eval_cmd?: string; execution_mode: 'until_done' }
  depends_on: string[]
  attempt: number
  failures: number
  max_attempts: number
  runner: string
  lease_until?: string
  heartbeat_at?: string
  progress: string
  last_error: string
  checkpoint: string
  checkpoint_attempt?: number
  checkpoint_at?: string
}

export interface HandoffDetail {
  handoff: Handoff
  messages: HandoffMessage[]
  research?: ResearchStatus
  next_before?: string
}

export interface HandoffPage {
  handoffs: Handoff[]
  next_before?: string
}

export interface HandoffCreateInput {
  project_slug?: string
  title: string
  description: string
  scope: string
  body: string
  target?: string
  draft: boolean
}

export interface RepoSync {
  synced_at: string
  error?: string
  default_branch?: string
  description?: string
  private?: boolean
  archived?: boolean
  head_sha?: string
  head_message?: string
  head_at?: string
  open_prs?: number
  latest_release?: string
  latest_release_at?: string
}

export interface ProjectRepo {
  id: string
  project_slug: string
  url: string
  provider: string
  repo: string
  web_url?: string
  branch?: string
  path?: string
  role?: string
  note?: string
  added_by: string
  created_at: string
  sync?: RepoSync
}

export interface RepoLinkInput {
  url: string
  role: string
  branch: string
  path: string
  note: string
}

export interface GitHubSyncStatus {
  configured: boolean
  hint?: string
  login?: string
  saved_at?: string
  last_run_at?: string
  last_error?: string
}

export interface ResearchCreateInput {
  project_slug?: string
  title: string
  objective: string
  acceptance: string[]
  deliverable: 'report' | 'answer' | 'dataset' | 'code'
  draft: boolean
}

export interface ResearchCreated {
  handoff_id: string
  message_id: string
  state: string
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

async function request<T>(method: 'GET' | 'POST' | 'PUT' | 'DELETE', path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined && !(body instanceof FormData)) {
    headers['Content-Type'] = 'application/json'
  }
  if (method !== 'GET') {
    headers['X-CSRF-Token'] = csrfToken ?? ''
  }
  const init: RequestInit = { method, headers, credentials: 'same-origin', cache: 'no-store' }
  if (body !== undefined) {
    init.body = body instanceof FormData ? body : JSON.stringify(body)
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

async function requestText(path: string): Promise<string> {
  const response = await fetch(`/admin/api${path}`, { headers: { Accept: 'text/markdown' }, credentials: 'same-origin', cache: 'no-store' })
  if (response.status === 401) {
    for (const listener of unauthorizedListeners) listener()
    throw new UnauthorizedError()
  }
  if (!response.ok) throw new ApiError(response.status, GENERIC_FAILURE)
  return response.text()
}

export const api = {
  reviewAuthorization: (query: string) => request<{ client_name: string; scopes: string[] }>('GET', `/oauth/authorize?${query}`),
  decideAuthorization: (query: string, action: 'approve' | 'deny') => request<{ redirect_url: string }>('POST', `/oauth/authorize?${query}`, { action }),
  changeApprovalPassword: (current_password: string, new_password: string) => request<void>('PUT', '/oauth/password', { current_password, new_password }),
  lookupDevice: (user_code: string) => request<DeviceRequest>('POST', '/oauth/device', { user_code, action: 'lookup' }),
  decideDevice: (user_code: string, action: 'approve' | 'deny') => request<void>('POST', '/oauth/device', { user_code, action }),
  getSession: () => request<Session & { authenticated: boolean }>('GET', '/session'),
  login: (password: string) => request<Session>('POST', '/login', { password }),
  logout: () => request<void>('POST', '/logout'),
  getOverview: () => request<Overview>('GET', '/overview'),
  listProjects: (tier?: Tier) => request<{ projects: Project[] }>('GET', tier ? `/projects?tier=${encodeURIComponent(tier)}` : '/projects').then((r) => r.projects),
  // The project page lists entries itself; this only needs the project record.
  getProject: (slug: string) => request<ProjectDetail>('GET', `/projects/${encodeURIComponent(slug)}?entries=1`).then((detail) => detail.project),
  saveProject: (slug: string, input: ProjectInput) => request<Project>('PUT', `/projects/${encodeURIComponent(slug)}`, input),
  setProjectResearch: (slug: string, visible: boolean) => request<{ research_visible: boolean }>('PUT', `/projects/${encodeURIComponent(slug)}/research`, { visible }),
  appendEntry: (slug: string, kind: string, body: string) => request<Entry>('POST', `/projects/${encodeURIComponent(slug)}/entries`, { kind, body }),
  listEntries: (filter: EntryFilter, before?: string) => request<EntryTablePage>('GET', `/entries${entryQuery(filter, { limit: '200', ...(before ? { before } : {}) })}`),
  getProjectSummaries: () => request<ProjectSummaries>('GET', '/table/projects'),
  resolveTodo: (id: string) => request<Entry & Undoable>('POST', `/entries/${encodeURIComponent(id)}/resolve`),
  inbox: () => request<InboxResponse>('GET', '/inbox'),
  setOwner: (id: string, patch: OwnerPatch) => request<OwnerState & Undoable>('POST', `/entries/${encodeURIComponent(id)}/owner`, patch),
  setLabels: (id: string, set: LabelPatch, reset: LabelField[] = []) => request<{ saved: true }>('POST', `/entries/${encodeURIComponent(id)}/labels`, { set, reset }),
  /** Marks every unread reading entry matching the filter as read, as one undoable action. */
  markAllRead: (filter: EntryFilter) => request<{ count: number; action_id?: string }>('POST', `/reading/read-all${entryQuery(filter)}`),
  deleteEntry: (id: string) => request<Undoable & { trash_id: string }>('DELETE', `/entries/${encodeURIComponent(id)}`),
  deletionPreview: (slug: string) => request<DeletionPreview>('GET', `/projects/${encodeURIComponent(slug)}/deletion`),
  deleteProject: (slug: string) => request<Undoable & { trash_id: string }>('DELETE', `/projects/${encodeURIComponent(slug)}`, { confirm: slug }),
  listActions: () => request<{ actions: OwnerAction[] }>('GET', '/actions').then((r) => r.actions),
  undoAction: (id: string) => request<{ undone: boolean }>('POST', `/actions/${encodeURIComponent(id)}/undo`),
  listTrash: () => request<{ items: TrashItem[] }>('GET', '/trash').then((r) => r.items),
  restoreTrash: (id: string) => request<{ restored: boolean }>('POST', `/trash/${encodeURIComponent(id)}/restore`),
  purgeTrash: (id: string) => request<void>('DELETE', `/trash/${encodeURIComponent(id)}`),
  getEntry: (id: string) => request<TableEntry & { repeats: TableEntry[]; repeats_total: number }>('GET', `/entries/${encodeURIComponent(id)}`),
  entryHistory: (id: string) => request<{ history: HistoryEvent[]; truncated?: boolean }>('GET', `/entries/${encodeURIComponent(id)}/history`),
  replyToEntry: (id: string, body: string) => request<Entry & Partial<Undoable>>('POST', `/entries/${encodeURIComponent(id)}/replies`, { body }),
  relatedEntries: (id: string) => request<{ related: RelatedEntry[] }>('GET', `/entries/${encodeURIComponent(id)}/related`).then((response) => response.related),
  reopenTodo: (id: string) => request<Entry & Undoable>('POST', `/entries/${encodeURIComponent(id)}/reopen`),
  entriesCsvUrl: (filter: EntryFilter) => `/admin/api/entries.csv${entryQuery(filter)}`,
  search: (input: SearchRequest) => request<SearchResponse>('POST', '/search', input),
  listAgents: () => request<{ agents: AgentSummary[] }>('GET', '/agents').then((response) => response.agents),
  listClients: (offset = 0) => request<ClientPage>('GET', `/oauth/clients?limit=50&offset=${offset}`),
  revokeClient: (clientId: string) => request<{ revoked: number }>('POST', '/oauth/revoke', { client_id: clientId }),
  listApiKeys: () => request<{ keys: ApiKey[] }>('GET', '/api-keys').then((page) => page.keys),
  createApiKey: (name: string) => request<{ key: ApiKey; secret: string }>('POST', '/api-keys', { name }),
  revokeApiKey: (id: number) => request<ApiKey>('DELETE', `/api-keys/${id}`),
  getCalendarConnection: () => request<CalendarConnection>('GET', '/calendar/connection'),
  startCalendarLogin: (serverUrl: string) => request<{ id: string; login_url: string }>('POST', '/calendar/connect', { server_url: serverUrl }),
  pollCalendarLogin: (id: string) => request<CalendarConnection & { pending?: boolean }>('POST', `/calendar/connect/${encodeURIComponent(id)}/poll`),
  disconnectCalendar: () => request<void>('DELETE', '/calendar/connection'),
  listCalendars: () => request<{ calendars: CalendarSource[] }>('GET', '/calendar/calendars').then((response) => response.calendars),
  selectCalendars: (ids: string[]) => request<{ selected: number }>('PUT', '/calendar/calendars', { ids }),
  listCalendarEvents: (start: string, end: string, calendarId = '') =>
    request<{ events: CalendarEvent[] }>('GET', `/calendar/events?start=${encodeURIComponent(start)}&end=${encodeURIComponent(end)}${calendarId ? `&calendar=${encodeURIComponent(calendarId)}` : ''}`).then((response) => response.events),
  getCalendarEvent: (id: string) => request<CalendarEvent>('GET', `/calendar/events/${encodeURIComponent(id)}`),
  createCalendarEvent: (calendarId: string, input: CalendarEventInput) => request<CalendarEvent>('POST', '/calendar/events', { calendar_id: calendarId, ...input }),
  updateCalendarEvent: (id: string, etag: string, input: CalendarEventInput) => request<CalendarEvent>('PUT', `/calendar/events/${encodeURIComponent(id)}`, { etag, ...input }),
  deleteCalendarEvent: (id: string, etag: string) => request<void>('DELETE', `/calendar/events/${encodeURIComponent(id)}`, { etag }),
  listHandoffs: (params: { q?: string; project?: string; status?: string; archive?: string; target?: string; before?: string } = {}) => {
    const query = new URLSearchParams()
    for (const [key, value] of Object.entries(params)) if (value) query.set(key, value)
    return request<HandoffPage>('GET', `/handoffs${query.size ? `?${query}` : ''}`)
  },
  getHandoff: (id: string, before?: string) => request<HandoffDetail>('GET', `/handoffs/${encodeURIComponent(id)}?messages=50${before ? `&before=${encodeURIComponent(before)}` : ''}`),
  createHandoff: (input: HandoffCreateInput) => request<HandoffDetail>('POST', '/handoffs', input),
  createResearch: (input: ResearchCreateInput) => request<ResearchCreated>('POST', '/research', input),
  saveHandoff: (id: string, input: Pick<HandoffCreateInput, 'project_slug' | 'title' | 'description' | 'scope'>) => request<Handoff>('PUT', `/handoffs/${encodeURIComponent(id)}`, input),
  appendHandoffMessage: (id: string, input: { body: string; target?: string; draft: boolean }) => request<HandoffMessage>('POST', `/handoffs/${encodeURIComponent(id)}/messages`, input),
  updateHandoffMessage: (id: string, action: string, target = '') => request<HandoffMessage>('POST', `/handoff-messages/${encodeURIComponent(id)}/actions`, { action, target }),
  uploadHandoffFile: (messageId: string, file: File) => {
    const form = new FormData()
    form.append('file', file)
    return request<HandoffFile>('POST', `/handoff-messages/${encodeURIComponent(messageId)}/files`, form)
  },
  deleteHandoffFile: (id: string) => request<void>('DELETE', `/handoff-files/${encodeURIComponent(id)}`),
  listProjectRepos: (slug: string) => request<{ repos: ProjectRepo[] }>('GET', `/projects/${encodeURIComponent(slug)}/repos`).then((response) => response.repos),
  linkProjectRepo: (slug: string, input: RepoLinkInput) => request<ProjectRepo>('POST', `/projects/${encodeURIComponent(slug)}/repos`, input),
  unlinkRepo: (id: string) => request<ProjectRepo>('DELETE', `/repos/${encodeURIComponent(id)}`),
  getGitHubSync: () => request<GitHubSyncStatus>('GET', '/github-sync'),
  setGitHubSync: (token: string) => request<GitHubSyncStatus>('PUT', '/github-sync', { token }),
  deleteGitHubSync: () => request<void>('DELETE', '/github-sync'),
  listProjectFiles: (slug: string) => request<{ files: HandoffFile[] }>('GET', `/projects/${encodeURIComponent(slug)}/files`).then((response) => response.files),
  exportHandoff: (id: string) => requestText(`/handoffs/${encodeURIComponent(id)}/export`),
}

export function describeError(error: unknown): string {
  if (error instanceof ApiError) return error.message
  return 'Something went wrong.'
}
