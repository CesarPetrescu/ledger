import { useMemo, useState, type FormEvent } from 'react'
import { api, describeError, ENTRY_KINDS, type EntryFilter, type OwnerPatch, type ProjectSummary, type TableEntry } from '../api'
import { useResource } from '../hooks/useResource'
import { Link, useLocation } from '../router'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Icon, KindBadge, Loading, StaleNotice, TierBadge, Timestamp } from '../components/ui'

const MAX_BODY = 4000
const LIVE = 'project entry entry_meta project_digest entry_owner_state'
type View = 'inbox' | 'projects' | 'todos' | 'decisions' | 'activity' | 'reading'
const VIEWS: { id: View; label: string }[] = [
  { id: 'inbox', label: 'Inbox' },
  { id: 'projects', label: 'Projects' },
  { id: 'todos', label: 'Todos' },
  { id: 'decisions', label: 'Decisions' },
  { id: 'activity', label: 'Activity' },
  { id: 'reading', label: 'Reading' },
]
const PRIORITY_ORDER: Record<string, number> = { high: 0, normal: 1, low: 2 }
const STATE_LABEL: Record<string, string> = { done: 'Done', in_progress: 'In progress', blocked: 'Blocked' }
const STALE_DAYS = 14
const dayFormat = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long' })
const shortDate = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short' })
const timeFormat = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' })

function dayLabel(iso: string, now = new Date()): string {
  const date = new Date(iso)
  const days = Math.round((new Date(now.toDateString()).getTime() - new Date(date.toDateString()).getTime()) / 86400000)
  if (days === 0) return 'Today'
  if (days === 1) return 'Yesterday'
  return dayFormat.format(date)
}

/** The extracted title, or the entry's first line until extraction catches up. */
function titleOf(entry: TableEntry): string {
  if (entry.meta?.title) return entry.meta.title
  const line = entry.body.trim().split('\n')[0] ?? ''
  return line.length > 110 ? `${line.slice(0, 109)}…` : line
}

/** Local calendar date as YYYY-MM-DD, to compare with due dates. */
function localDay(date = new Date()): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function isStale(entry: TableEntry, now = Date.now()): boolean {
  return entry.kind === 'todo' && !entry.resolved_by && now - Date.parse(entry.created_at) > STALE_DAYS * 86400000
}

function groupBy<T>(items: T[], key: (item: T) => string): { key: string; items: T[] }[] {
  const groups: { key: string; items: T[] }[] = []
  for (const item of items) {
    const k = key(item)
    const last = groups.at(-1)
    if (last?.key === k) last.items.push(item)
    else groups.push({ key: k, items: [item] })
  }
  return groups
}

/**
 * Folds repeats into the newest loaded copy of the same story. The server
 * links every repeat directly to its root, so the root ID groups a story even
 * when the root itself is older than the loaded page.
 */
function foldRepeats(entries: TableEntry[]): { entry: TableEntry; repeats: TableEntry[] }[] {
  const heads = new Map<string, { entry: TableEntry; repeats: TableEntry[] }>()
  const out: { entry: TableEntry; repeats: TableEntry[] }[] = []
  for (const entry of entries) {
    const key = entry.duplicate_of ?? entry.id
    const head = heads.get(key)
    if (head) head.repeats.push(entry)
    else {
      const item = { entry, repeats: [] as TableEntry[] }
      heads.set(key, item)
      out.push(item)
    }
  }
  return out
}

/** Project option label; adds the slug when another project shares the name. */
function projectLabel(project: { slug: string; name: string }, all: { slug: string; name: string }[]): string {
  return all.some((other) => other.slug !== project.slug && other.name === project.name) ? `${project.name} (${project.slug})` : project.name
}

/** Runs an owner triage action (read, star, handle, snooze) with feedback. */
function useOwnerAction(onChanged: () => void) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const act = async (id: string, patch: OwnerPatch, message: string) => {
    setBusy(true)
    try {
      await api.setOwner(id, patch)
      toast(message)
      onChanged()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  return { busy, act }
}

function RelatedList({ id }: { id: string }) {
  const related = useResource(() => api.relatedEntries(id), `related:${id}`, 'entry_meta')
  if (related.loading || !related.data || related.data.length === 0) return null
  return (
    <div className="related">
      <p className="related-title">Related</p>
      <ul>
        {related.data.map((entry) => (
          <li key={entry.id}>
            <Link to={`/table?view=activity&project=${encodeURIComponent(entry.slug)}&q=${encodeURIComponent(titleOf(entry))}`}>{titleOf(entry)}</Link>
            <span className="muted small"> · {entry.project_name} · <Timestamp iso={entry.created_at} /></span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function AddRow({ projects, initialProject, defaultKind, onAdded }: { projects: { slug: string; name: string }[]; initialProject: string; defaultKind: string; onAdded: () => void }) {
  const [slug, setSlug] = useState(initialProject)
  const [kind, setKind] = useState(defaultKind)
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const project = slug || projects[0]?.slug || ''

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!project || !body.trim() || busy) return
    setBusy(true)
    setError('')
    try {
      await api.appendEntry(project, kind, body.trim())
      setBody('')
      toast('Row added.')
      onAdded()
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }

  return (
    <details className="table-add-toggle">
      <summary><Icon name="plus" /> Add a {defaultKind === 'todo' ? 'todo' : 'row'}</summary>
      <form className="table-add" aria-label="Add row" onSubmit={(event) => void submit(event)}>
        <label>Project<select value={project} onChange={(event) => setSlug(event.target.value)}>{projects.map((item) => <option key={item.slug} value={item.slug}>{projectLabel(item, projects)}</option>)}</select></label>
        <label>Kind<select value={kind} onChange={(event) => setKind(event.target.value)}>{ENTRY_KINDS.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
        <label className="table-add-body">Text<input value={body} maxLength={MAX_BODY} onChange={(event) => setBody(event.target.value)} placeholder="New note, todo, decision, or status…" /></label>
        <button type="submit" className="btn btn-primary" disabled={!project || !body.trim() || busy}><Icon name="plus" /> {busy ? 'Adding…' : 'Add'}</button>
        {error && <p className="field-error" role="alert">{error}</p>}
      </form>
    </details>
  )
}

function TodoState({ entry, onChanged }: { entry: TableEntry; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const act = async (action: 'done' | 'reopen') => {
    setBusy(true)
    try {
      if (action === 'done') await api.resolveTodo(entry.id)
      else await api.reopenTodo(entry.id)
      toast(action === 'done' ? 'Todo marked done.' : 'Todo reopened.')
      onChanged()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  if (!entry.resolved_by) {
    return <button type="button" className="btn btn-small" disabled={busy} onClick={() => void act('done')}>Mark done</button>
  }
  return (
    <span className="todo-done">
      <span title={entry.resolved_by.origin === 'model' ? 'An agent reported this finished; detected automatically' : undefined}>
        Done{entry.resolved_by.origin === 'model' ? ' (detected)' : ''} <Timestamp iso={entry.resolved_by.created_at} />
      </span>
      <button type="button" className="link-button" disabled={busy} onClick={() => void act('reopen')}>Reopen</button>
    </span>
  )
}

/** Short facts shown beside the title so the row never needs the full text. */
function FocusBadges({ entry }: { entry: TableEntry }) {
  const meta = entry.meta
  const today = localDay()
  return (
    <span className="focus-badges">
      {meta?.ask && !entry.owner.handled && <span className="badge" data-focus="ask">Asks you</span>}
      {meta?.importance === 'important' && <span className="badge" data-focus="important">Important</span>}
      {meta?.state && <span className="badge" data-state={meta.state}>{STATE_LABEL[meta.state]}</span>}
      {entry.kind === 'todo' && meta?.priority && meta.priority !== 'normal' && <span className="badge" data-priority={meta.priority}>{meta.priority}</span>}
      {meta?.size && <span className="badge" data-focus="size" title="Estimated size">{meta.size}</span>}
      {meta?.due && !entry.resolved_by && (
        <span className="badge" data-focus={meta.due < today ? 'overdue' : 'due'}>
          {meta.due < today ? 'Overdue' : 'Due'} {shortDate.format(new Date(`${meta.due}T12:00:00`))}
        </span>
      )}
      {isStale(entry) && <span className="badge" data-focus="stale" title={`Open for more than ${STALE_DAYS} days`}>Stale</span>}
    </span>
  )
}

function Facts({ entry }: { entry: TableEntry }) {
  const meta = entry.meta
  if (!meta) return null
  const facts: [string, string][] = [
    ['Asks you', meta.ask ?? ''],
    ['Next step', meta.next_step ?? ''],
    ['Blocked by', meta.blocker ?? ''],
    [entry.kind === 'decision' ? 'Why' : 'Why it matters', meta.why ?? ''],
    ['Source', meta.source ?? ''],
  ].filter((fact): fact is [string, string] => fact[1] !== '')
  if (facts.length === 0 && !meta.link) return null
  return (
    <dl className="facts">
      {facts.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
      {meta.link && (
        <div>
          <dt>Link</dt>
          <dd><a href={meta.link} target="_blank" rel="noopener noreferrer">{meta.link}</a></dd>
        </div>
      )}
    </dl>
  )
}

function EntryRow({ entry, repeats = [], view, headline, onTag, onChanged }: { entry: TableEntry; repeats?: TableEntry[]; view: View; headline?: string; onTag: (tag: string) => void; onChanged: () => void }) {
  const [open, setOpen] = useState(false)
  const [showRepeats, setShowRepeats] = useState(false)
  const owner = useOwnerAction(onChanged)
  const pending = !entry.meta
  const reading = view === 'reading'
  const summary = reading ? entry.meta?.why || entry.meta?.gist : entry.meta?.gist
  const asking = Boolean(entry.meta?.ask && !entry.owner.handled)
  return (
    <li className="entry-row" data-done={entry.resolved_by ? 'true' : undefined} data-read={reading && entry.owner.read ? 'true' : undefined}>
      <div className="entry-row-main">
        <div className="entry-row-head">
          <button type="button" className="entry-row-title" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
            {headline ?? titleOf(entry)}
          </button>
          <FocusBadges entry={entry} />
        </div>
        {headline ? <p className="entry-row-gist">{titleOf(entry)}</p> : summary && <p className="entry-row-gist">{summary}</p>}
        <div className="entry-row-meta">
          {view === 'activity' && <KindBadge kind={entry.kind} />}
          {entry.meta?.tags.map((tag) => <button key={tag} type="button" className="chip" onClick={() => onTag(tag)} aria-label={`Filter by tag ${tag}`}>{tag}</button>)}
          {reading && entry.meta?.source && <span>{entry.meta.source}</span>}
          {view !== 'todos' && <Link to={`/projects/${entry.slug}`}>{entry.project_name}</Link>}
          <code>{entry.source}</code>
          {repeats.length > 0 && (
            <button type="button" className="chip chip-repeat" aria-expanded={showRepeats} onClick={() => setShowRepeats((value) => !value)}>
              +{repeats.length} {repeats.length === 1 ? 'repeat' : 'repeats'}
            </button>
          )}
          {view === 'todos' || view === 'inbox' ? <Timestamp iso={entry.created_at} /> : <time dateTime={entry.created_at} title={new Date(entry.created_at).toLocaleString()}>{timeFormat.format(new Date(entry.created_at))}</time>}
          {reading && entry.meta?.link && <a href={entry.meta.link} target="_blank" rel="noopener noreferrer">Open <Icon name="external" /></a>}
        </div>
        {showRepeats && (
          <ul className="repeats" aria-label="Repeats">
            {repeats.map((repeat) => <li key={repeat.id}>{titleOf(repeat)} <span className="muted">· {repeat.source} · <Timestamp iso={repeat.created_at} /></span></li>)}
          </ul>
        )}
        {open && (
          <div className="entry-row-detail">
            <Facts entry={entry} />
            <p className="entry-body">{entry.body}</p>
            {entry.meta && entry.meta.refs.length > 0 && <ul className="refs" aria-label="References">{entry.meta.refs.map((ref) => <li key={ref}><code>{ref}</code></li>)}</ul>}
            <p className="muted small">{pending ? 'Summary pending.' : entry.meta?.origin === 'model' ? 'Title, summary, and tags generated by AI from the text above.' : 'Written from the console.'}</p>
            <RelatedList id={entry.id} />
          </div>
        )}
      </div>
      <div className="entry-row-actions">
        {entry.kind === 'todo' && <TodoState entry={entry} onChanged={onChanged} />}
        {asking && <button type="button" className="btn btn-small" disabled={owner.busy} onClick={() => void owner.act(entry.id, { handled: true }, 'Marked handled.')}>Handled</button>}
        {(asking || (entry.kind === 'todo' && !entry.resolved_by)) && view === 'inbox' && (
          <button type="button" className="link-button" disabled={owner.busy} onClick={() => void owner.act(entry.id, { snooze_days: 1 }, 'Snoozed until tomorrow.')}>Snooze</button>
        )}
        {reading && (
          <>
            <button type="button" className="btn btn-small" disabled={owner.busy} onClick={() => void owner.act(entry.id, { read: !entry.owner.read }, entry.owner.read ? 'Marked unread.' : 'Marked read.')}>
              {entry.owner.read ? 'Mark unread' : 'Mark read'}
            </button>
            <button type="button" className="star-button" aria-pressed={entry.owner.starred} aria-label={entry.owner.starred ? 'Unstar' : 'Star'} disabled={owner.busy}
              onClick={() => void owner.act(entry.id, { starred: !entry.owner.starred }, entry.owner.starred ? 'Unstarred.' : 'Starred.')}>
              {entry.owner.starred ? '★' : '☆'}
            </button>
          </>
        )}
      </div>
    </li>
  )
}

/** Consecutive entries by the same agent on the same project collapse into one run. */
type Folded = { entry: TableEntry; repeats: TableEntry[] }

function ActivityRun({ items, onTag, onChanged }: { items: Folded[]; onTag: (tag: string) => void; onChanged: () => void }) {
  const [expanded, setExpanded] = useState(false)
  const [first, ...rest] = items
  if (!first) return null
  return (
    <>
      <EntryRow entry={first.entry} repeats={first.repeats} view="activity" onTag={onTag} onChanged={onChanged} />
      {rest.length > 0 && !expanded && (
        <li className="entry-run-more">
          <button type="button" className="link-button" onClick={() => setExpanded(true)}>
            +{rest.length} more from {first.entry.source} on {first.entry.project_name}
          </button>
        </li>
      )}
      {expanded && rest.map((item) => <EntryRow key={item.entry.id} entry={item.entry} repeats={item.repeats} view="activity" onTag={onTag} onChanged={onChanged} />)}
    </>
  )
}

function EntriesView({ view, initialProject, initialQuery }: { view: Exclude<View, 'projects' | 'inbox'>; initialProject: string; initialQuery: string }) {
  const [filter, setFilter] = useState<EntryFilter>({
    project: initialProject, q: initialQuery, status: view === 'todos' ? 'open' : '', reading: view === 'reading' ? 'unread' : '',
    // Routine bookkeeping stays out of the way unless asked for (or searched).
    hide_routine: initialQuery ? '' : '1',
  })
  const [loadingMore, setLoadingMore] = useState(false)
  const effective: EntryFilter = {
    ...filter,
    kind: view === 'todos' ? 'todo' : view === 'decisions' ? 'decision' : view === 'reading' ? '' : filter.kind ?? '',
    status: view === 'todos' ? filter.status ?? '' : '',
    reading: view === 'reading' ? filter.reading || 'unread' : '',
    hide_routine: view === 'todos' || view === 'reading' ? '' : filter.hide_routine ?? '',
  }
  const key = `entries:${view}:${JSON.stringify(effective)}`
  const table = useResource(() => api.listEntries(effective), key, LIVE)
  const projects = useResource(() => api.listProjects(), 'table-projects', 'project')
  const toast = useToast()
  const set = (field: keyof EntryFilter, value: string) => setFilter((current) => ({ ...current, [field]: value }))

  // Todos read best per project, most important first. Fold while the list
  // is still newest first so each head is the newest copy, then sort heads.
  const folded = useMemo(() => {
    const heads = foldRepeats(table.data?.entries ?? [])
    if (view !== 'todos') return heads
    return [...heads].sort((a, b) => a.entry.project_name.localeCompare(b.entry.project_name) || a.entry.slug.localeCompare(b.entry.slug) || (PRIORITY_ORDER[a.entry.meta?.priority ?? 'normal'] ?? 1) - (PRIORITY_ORDER[b.entry.meta?.priority ?? 'normal'] ?? 1))
  }, [table.data, view])
  // Todos group by project slug (names may repeat); other views by day.
  const groups = useMemo(() => groupBy(folded, (item) => (view === 'todos' ? item.entry.slug : dayLabel(item.entry.created_at))), [folded, view])
  const count = table.data?.entries.length ?? 0

  const loadMore = async () => {
    const snapshot = table.data
    const cursor = snapshot?.next_before
    if (!cursor || loadingMore) return
    setLoadingMore(true)
    try {
      const page = await api.listEntries(effective, cursor)
      // Any reload, filter change, or live update while this was in flight
      // replaced the list object; appending the old page would mix or skip rows.
      table.update((current) => (current === snapshot ? { ...page, entries: [...current.entries, ...page.entries] } : current))
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setLoadingMore(false)
    }
  }

  const empty = view === 'todos' && filter.status === 'open' ? 'No open todos. Nice.'
    : view === 'reading' && effective.reading === 'unread' ? 'Nothing left to read.'
    : 'No entries match.'

  return (
    <>
      <div className="filters table-filters">
        <label><span className="visually-hidden">Search text</span><input type="search" maxLength={1000} placeholder="Search titles and text" value={filter.q ?? ''} onChange={(event) => set('q', event.target.value)} /></label>
        <label><span className="visually-hidden">Filter by project</span><select value={filter.project ?? ''} onChange={(event) => set('project', event.target.value)}><option value="">Any project</option>{(projects.data ?? []).map((item) => <option key={item.slug} value={item.slug}>{projectLabel(item, projects.data ?? [])}</option>)}</select></label>
        {view === 'todos' && <label><span className="visually-hidden">Filter by state</span><select value={filter.status ?? ''} onChange={(event) => set('status', event.target.value)}><option value="open">Open</option><option value="done">Done</option><option value="">Open and done</option></select></label>}
        {view === 'reading' && <label><span className="visually-hidden">Filter reading</span><select value={effective.reading} onChange={(event) => set('reading', event.target.value)}><option value="unread">Unread</option><option value="starred">Starred</option><option value="all">All</option></select></label>}
        {view === 'activity' && <label><span className="visually-hidden">Filter by kind</span><select value={filter.kind ?? ''} onChange={(event) => set('kind', event.target.value)}><option value="">Any kind</option>{ENTRY_KINDS.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>}
        <label><span className="visually-hidden">Filter by agent</span><select value={filter.source ?? ''} onChange={(event) => set('source', event.target.value)}><option value="">Any agent</option>{(table.data?.sources ?? []).map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
        <label><span className="visually-hidden">Filter by tag</span><select value={filter.tag ?? ''} onChange={(event) => set('tag', event.target.value)}><option value="">Any tag</option>{[...new Set([...(filter.tag ? [filter.tag] : []), ...(table.data?.tags ?? [])])].map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      </div>
      {(view === 'activity' || view === 'decisions') && (
        <label className="check"><input type="checkbox" checked={!filter.hide_routine} onChange={(event) => set('hide_routine', event.target.checked ? '' : '1')} /> Show routine entries</label>
      )}
      {view !== 'decisions' && view !== 'reading' && (projects.data?.length ?? 0) > 0 && <AddRow key={view} projects={projects.data ?? []} initialProject={initialProject} defaultKind={view === 'todos' ? 'todo' : 'note'} onAdded={table.reload} />}
      {table.loading && <Loading label="Loading entries…" />}
      {!table.loading && !table.data && <ErrorState message="Couldn't load entries." onRetry={table.reload} />}
      {table.stale && <StaleNotice message="Table may be out of date." onRetry={table.reload} />}
      {table.data && count === 0 && <EmptyState><p>{empty}</p></EmptyState>}
      {groups.map((group) => (
        <section key={group.key} className="entry-group" aria-label={view === 'todos' ? group.items[0]?.entry.project_name : group.key}>
          <h2 className="entry-group-title">
            {view === 'todos' ? group.items[0]?.entry.project_name : group.key} {view === 'todos' && <code className="muted">{group.key}</code>} <span className="count">{group.items.length}</span>
          </h2>
          <ul className="entry-list">
            {view === 'activity'
              ? groupBy(group.items, (item) => `${item.entry.slug}\u0000${item.entry.source}`).map((run) => <ActivityRun key={run.items[0]?.entry.id} items={run.items} onTag={(tag) => set('tag', tag)} onChanged={table.reload} />)
              : group.items.map((item) => <EntryRow key={item.entry.id} entry={item.entry} repeats={item.repeats} view={view} onTag={(tag) => set('tag', tag)} onChanged={table.reload} />)}
          </ul>
        </section>
      ))}
      {table.data && count > 0 && (
        <p className="muted small table-foot">
          {count} {count === 1 ? 'row' : 'rows'} loaded{table.data.next_before ? ' · more available' : ''} ·{' '}
          <a href={api.entriesCsvUrl(effective)} download>Download CSV</a>
        </p>
      )}
      {table.data?.next_before && <button type="button" className="btn" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Load more rows'}</button>}
    </>
  )
}

/**
 * A project's health label. Only "blocked" is shown: a latest status of "done"
 * means that update finished something, not that the project did.
 */
function HealthBadge({ state }: { state: string }) {
  if (state !== 'blocked') return null
  return <span className="badge" data-state="blocked">Blocked</span>
}

/** Everything that needs the owner, most urgent first. */
function InboxView() {
  const inbox = useResource(api.inbox, 'inbox', LIVE)
  const noop = () => {}
  if (inbox.loading) return <Loading label="Loading inbox…" />
  if (!inbox.data) return <ErrorState message="Couldn't load the inbox." onRetry={inbox.reload} />
  const { needs_you: asks, todos, todos_total: total, projects } = inbox.data
  const blocked = projects.filter((project) => project.status_state === 'blocked')
  const digests = projects.filter((project) => project.digest)
  return (
    <div className="inbox">
      {inbox.stale && <StaleNotice message="Inbox may be out of date." onRetry={inbox.reload} />}
      <section className="entry-group" aria-label="Needs you">
        <h2 className="entry-group-title">Needs you <span className="count">{asks.length}</span></h2>
        {asks.length === 0 ? <p className="muted">Nothing is waiting on you.</p> : (
          <ul className="entry-list">
            {asks.map((entry) => <EntryRow key={entry.id} entry={entry} view="inbox" headline={entry.meta?.ask ?? titleOf(entry)} onTag={noop} onChanged={inbox.reload} />)}
          </ul>
        )}
      </section>
      <section className="entry-group" aria-label="Todos">
        <h2 className="entry-group-title">
          Todos <span className="count">{todos.length < total ? `${todos.length} of ${total}` : total}</span>
          {total > 0 && <Link className="section-link" to="/table?view=todos">All todos</Link>}
        </h2>
        {todos.length === 0 ? <p className="muted">No open todos.</p> : (
          <ul className="entry-list">
            {todos.map((entry) => <EntryRow key={entry.id} entry={entry} view="inbox" onTag={noop} onChanged={inbox.reload} />)}
          </ul>
        )}
      </section>
      {blocked.length > 0 && (
        <section className="entry-group" aria-label="Blocked">
          <h2 className="entry-group-title">Blocked <span className="count">{blocked.length}</span></h2>
          <ul className="project-lines">
            {blocked.map((project) => (
              <li key={project.slug}>
                <Link to={`/table?view=activity&project=${encodeURIComponent(project.slug)}`}>{project.name}</Link>
                <span className="muted"> · {project.status_title || project.status_body}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {digests.length > 0 && (
        <section className="entry-group" aria-label="This week">
          <h2 className="entry-group-title">This week</h2>
          <ul className="project-lines">
            {digests.map((project) => (
              <li key={project.slug}>
                <Link to={`/table?view=activity&project=${encodeURIComponent(project.slug)}`}>{project.name}</Link> <HealthBadge state={project.status_state} />
                <p className="clamp digest">{project.digest}</p>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function ProjectsView({ projects }: { projects: ProjectSummary[] }) {
  if (projects.length === 0) {
    return <EmptyState><p>No projects yet.</p><Link className="btn btn-primary" to="/projects/_new">Create a project</Link></EmptyState>
  }
  return (
    <table className="table summary-table">
      <thead>
        <tr>
          <th scope="col">Project</th>
          <th scope="col">This week</th>
          <th scope="col" className="num">Open todos</th>
          <th scope="col">Last 7 days</th>
          <th scope="col">Deadline</th>
        </tr>
      </thead>
      <tbody>
        {projects.map((project) => (
          <tr key={project.slug}>
            <td data-label="Project"><div>
              <Link to={`/table?view=activity&project=${encodeURIComponent(project.slug)}`}>{project.name}</Link> <TierBadge tier={project.tier} /> <HealthBadge state={project.status_state} />
              {project.needs_you > 0 && <p className="small"><Link to="/table?view=inbox">{project.needs_you} {project.needs_you === 1 ? 'thing needs' : 'things need'} you</Link></p>}
              {project.needs_me && <p className="small needs-me clamp" title={project.needs_me}>Needs you: {project.needs_me}</p>}
            </div></td>
            <td data-label="This week"><div>
              {project.digest ? <p className="clamp digest" title={project.digest}>{project.digest}</p> : !project.status_at && <span className="muted">No status yet</span>}
              {project.status_at && (
                <p className={project.digest ? 'muted small latest-status' : 'status-text'}>
                  {project.digest && 'Latest: '}<span className={project.digest ? undefined : 'clamp'}>{project.status_title || project.status_body}</span>
                  <span className="muted small"> · <Timestamp iso={project.status_at} /> · {project.status_source}</span>
                </p>
              )}
            </div></td>
            <td data-label="Open todos" className="num"><div>
              {project.open_todos > 0 ? <Link to={`/table?view=todos&project=${encodeURIComponent(project.slug)}`}>{project.open_todos}</Link> : <span className="muted">0</span>}
            </div></td>
            <td data-label="Last 7 days"><div>
              {project.week_entries > 0 ? (
                <>
                  {project.week_entries} {project.week_entries === 1 ? 'entry' : 'entries'}
                  <span className="agents">{project.week_agents.map((agent) => <code key={agent}>{agent}</code>)}</span>
                </>
              ) : <span className="muted">Quiet{project.last_entry_at ? <> · last <Timestamp iso={project.last_entry_at} /></> : ''}</span>}
            </div></td>
            <td data-label="Deadline"><div>{project.deadline || <span className="muted">—</span>}</div></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export function TablePage() {
  const { query } = useLocation()
  const requested = query.get('view')
  const view: View = VIEWS.some((item) => item.id === requested) ? (requested as View) : 'inbox'
  const project = query.get('project') ?? ''
  const search = query.get('q') ?? ''
  const summaries = useResource(api.getProjectSummaries, 'table-summaries', LIVE)
  const progress = summaries.data?.metadata
  // Only promise progress while an extractor is alive to make it.
  const extracting = progress?.active && progress.total > 0 && progress.ready + progress.failed < progress.total

  return (
    <>
      <header className="page-head">
        <div>
          <p className="eyebrow">What your agents are doing</p>
          <h1>Table</h1>
        </div>
        {extracting && <p className="muted small" role="status">AI summaries: {progress.ready} of {progress.total} entries processed. New ones appear as they finish.</p>}
      </header>
      <nav className="view-tabs" aria-label="Table views">
        {VIEWS.map((item) => (
          <Link key={item.id} to={`/table?view=${item.id}`} aria-current={view === item.id ? 'page' : undefined}>
            {item.label}
          </Link>
        ))}
      </nav>
      {view === 'inbox' ? <InboxView />
        : view === 'projects' ? (
          summaries.loading ? <Loading label="Loading projects…" />
            : !summaries.data ? <ErrorState message="Couldn't load projects." onRetry={summaries.reload} />
            : <ProjectsView projects={summaries.data.projects} />
        ) : (
          <EntriesView key={`${view}:${project}:${search}`} view={view} initialProject={project} initialQuery={search} />
        )}
    </>
  )
}
