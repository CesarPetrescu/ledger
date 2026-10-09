import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { api, describeError, ENTRY_KINDS, OWNER_SOURCE, type EntryFilter, type OwnerPatch, type TableEntry } from '../api'
import { useResource } from '../hooks/useResource'
import { useUndo } from '../hooks/useUndo'
import { refreshAll } from '../live'
import { ConfirmDialog } from './ConfirmDialog'
import { useEntrySelection, writerName } from './EntryPanel'
import { plainText } from './Markdown'
import { OverflowMenu } from './OverflowMenu'
import { Link } from '../router'
import { useToast } from './Toast'
import { EmptyState, ErrorState, Icon, KindBadge, Loading, StaleNotice, Timestamp } from './ui'

// Entry lists shared by the Inbox, the Table, project pages, and entry pages.

const MAX_BODY = 4000
export const LIVE = 'project entry entry_meta project_digest entry_owner_state owner_action trash'
/** The entry lists: every view shows entries, the inbox with triage actions. */
export type ListView = 'todos' | 'decisions' | 'activity' | 'reading'
type RowView = ListView | 'inbox'
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

/** The extracted title, or the entry's first line until extraction catches up; plain text either way. */
export function titleOf(entry: TableEntry): string {
  if (entry.meta?.title) return plainText(entry.meta.title)
  const line = plainText(entry.body.trim().split('\n')[0] ?? '')
  return line.length > 110 ? `${line.slice(0, 109)}…` : line
}

/** Local calendar date as YYYY-MM-DD, to compare with due dates. */
function localDay(date = new Date()): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

export function daysUntil(iso: string, now = Date.now()): number {
  return Math.max(0, Math.ceil((Date.parse(iso) - now) / 86400000))
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
export function projectLabel(project: { slug: string; name: string }, all: { slug: string; name: string }[]): string {
  return all.some((other) => other.slug !== project.slug && other.name === project.name) ? `${project.name} (${project.slug})` : project.name
}

/** Runs an owner triage action (read, star, handle, snooze) with feedback. */
export function useOwnerAction(onChanged: () => void) {
  const toast = useToast()
  const undo = useUndo()
  const [busy, setBusy] = useState(false)
  const act = async (id: string, patch: OwnerPatch, message: string) => {
    setBusy(true)
    try {
      const result = await api.setOwner(id, patch)
      undo(message, result.action_id)
      onChanged()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  return { busy, act }
}

export function RelatedList({ id }: { id: string }) {
  const related = useResource(() => api.relatedEntries(id), `related:${id}`, 'entry_meta')
  if (related.loading || !related.data || related.data.length === 0) return null
  return (
    <div className="related">
      <p className="related-title">Related</p>
      <ul>
        {related.data.map((entry) => (
          <li key={entry.id}>
            <Link to={`/entries/${entry.id}`}>{titleOf(entry)}</Link>
            <span className="muted small"> · {entry.project_name} · <Timestamp iso={entry.created_at} /></span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Adds an entry; on a project page (fixed) the project is not asked for. */
function AddRow({ projects, initialProject, defaultKind, fixed = false, onAdded }: { projects: { slug: string; name: string }[]; initialProject: string; defaultKind: string; fixed?: boolean; onAdded: () => void }) {
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
      toast('Entry added.')
      onAdded()
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }

  return (
    <details className="table-add-toggle">
      <summary><Icon name="plus" /> Add {defaultKind === 'todo' ? 'a todo' : defaultKind === 'decision' ? 'a decision' : 'an entry'}</summary>
      <form className="table-add" data-fixed={fixed ? 'true' : undefined} aria-label="Add entry" onSubmit={(event) => void submit(event)}>
        {!fixed && <label>Project<select value={project} onChange={(event) => setSlug(event.target.value)}>{projects.map((item) => <option key={item.slug} value={item.slug}>{projectLabel(item, projects)}</option>)}</select></label>}
        <label>Kind<select value={kind} onChange={(event) => setKind(event.target.value)}>{ENTRY_KINDS.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
        <label className="table-add-body">Text<textarea value={body} rows={2} maxLength={MAX_BODY} onChange={(event) => setBody(event.target.value)} placeholder="What happened, what was decided, or what is next…" /></label>
        <button type="submit" className="btn btn-primary" disabled={!project || !body.trim() || busy}><Icon name="plus" /> {busy ? 'Adding…' : 'Add'}</button>
        <p className="muted small table-add-note">Entries can't be edited later; add a correction as a new entry. {body.length > MAX_BODY - 400 && <span>{body.length}/{MAX_BODY}</span>}</p>
        {error && <p className="field-error" role="alert">{error}</p>}
      </form>
    </details>
  )
}

export function TodoState({ entry, onChanged }: { entry: TableEntry; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const undo = useUndo()
  const act = async (action: 'done' | 'reopen') => {
    setBusy(true)
    try {
      const result = action === 'done' ? await api.resolveTodo(entry.id) : await api.reopenTodo(entry.id)
      undo(action === 'done' ? 'Todo marked done.' : 'Todo reopened.', result.action_id)
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
export function FocusBadges({ entry }: { entry: TableEntry }) {
  const meta = entry.meta
  const today = localDay()
  const priority = entry.kind === 'todo' ? meta?.priority : undefined
  return (
    <span className="focus-badges">
      {meta?.ask && !entry.owner.handled && entry.source !== OWNER_SOURCE && <span className="badge" data-focus="ask">Asks you</span>}
      {/* One emphasis at most: a high priority outranks importance, which outranks a low priority. */}
      {priority === 'high' ? <span className="badge" data-priority="high">High</span>
        : meta?.importance === 'important' ? <span className="badge" data-focus="important">Important</span>
        : priority === 'low' ? <span className="badge" data-priority="low">Low</span> : null}
      {meta?.state && <span className="badge" data-state={meta.state}>{STATE_LABEL[meta.state]}</span>}
      {meta?.size && <span className="badge" data-focus="size" title="Estimated size">{meta.size}</span>}
      {meta?.due && !entry.resolved_by && (
        <span className="badge" data-focus={meta.due < today ? 'overdue' : 'due'}>
          {meta.due < today ? 'Overdue' : 'Due'} {shortDate.format(new Date(`${meta.due}T12:00:00`))}
        </span>
      )}
      {isStale(entry) && <span className="badge" data-focus="stale" title={`Open for more than ${STALE_DAYS} days`}>Stale</span>}
      {meta?.unsure && meta.unsure.length > 0 && <span className="badge" data-focus="unsure" title={`The AI was unsure of: ${meta.unsure.join(', ').replaceAll('_', ' ')}. Open the entry to check or edit its labels.`}>Check</span>}
    </span>
  )
}

/** The entry's ⋯ menu: Delete, after confirmation; it goes to Trash and can be undone. */
export function DeleteEntry({ entry, onChanged }: { entry: TableEntry; onChanged: () => void }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const undo = useUndo()
  const confirm = async () => {
    setBusy(true)
    try {
      const result = await api.deleteEntry(entry.id)
      setOpen(false)
      undo('Entry moved to Trash.', result.action_id)
      onChanged()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <OverflowMenu label="More actions for this entry" items={[{ label: 'Delete entry', danger: true, onSelect: () => setOpen(true) }]} />
      <ConfirmDialog open={open} title="Delete this entry?" confirmLabel="Delete entry" busy={busy} onConfirm={() => void confirm()} onCancel={() => setOpen(false)}>
        <p>“{titleOf(entry)}” moves to Trash for 30 days. Agents and search stop seeing it. You can undo right away or restore it from Trash.</p>
      </ConfirmDialog>
    </>
  )
}

export function Facts({ entry, hideAsk = false }: { entry: TableEntry; hideAsk?: boolean }) {
  const meta = entry.meta
  if (!meta) return null
  const facts: [string, string][] = [
    ['Asks you', hideAsk ? '' : meta.ask ?? ''],
    ['Next step', meta.next_step ?? ''],
    ['Blocked by', meta.blocker ?? ''],
    [entry.kind === 'decision' ? 'Why' : 'Why it matters', meta.why ?? ''],
    ['Chose', meta.details?.chosen ?? ''],
    ['Turned down', meta.details?.rejected ?? ''],
    ['Category', meta.category ?? ''],
    ['Key numbers', (meta.details?.numbers ?? []).map((n) => `${n.label}: ${n.value}`).join(' · ')],
    ['About', (meta.details?.entities ?? []).join(', ')],
    ['Source', meta.source ?? ''],
  ].filter((fact): fact is [string, string] => fact[1] !== '')
  const checklist = meta.details?.checklist ?? []
  const links = (meta.details?.links ?? []).filter((link) => link !== meta.link)
  if (facts.length === 0 && !meta.link && checklist.length === 0 && links.length === 0) return null
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
      {checklist.length > 0 && (
        <div>
          <dt>Checklist</dt>
          <dd>
            <ul className="checklist">
              {checklist.map((item, index) => <li key={index} data-done={item.done ? 'true' : undefined}><span aria-hidden="true">{item.done ? '☑' : '☐'}</span> {item.text}{item.done && <span className="visually-hidden"> (done)</span>}</li>)}
            </ul>
          </dd>
        </div>
      )}
      {links.length > 0 && (
        <div>
          <dt>Links</dt>
          <dd>
            <ul className="checklist">
              {links.map((link) => <li key={link}><a href={link} target="_blank" rel="noopener noreferrer">{link}</a></li>)}
            </ul>
          </dd>
        </div>
      )}
    </dl>
  )
}

export function EntryRow({ entry, repeats = [], view, headline, hideProject = false, onChanged }: { entry: TableEntry; repeats?: TableEntry[]; view: RowView; headline?: string; hideProject?: boolean; onTag?: (tag: string) => void; onChanged: () => void }) {
  const selection = useEntrySelection()
  const owner = useOwnerAction(onChanged)
  const reading = view === 'reading'
  const summary = plainText((reading ? entry.meta?.why || entry.meta?.gist : entry.meta?.gist) ?? '')
  const asking = Boolean(entry.meta?.ask && !entry.owner.handled && entry.source !== OWNER_SOURCE)
  const selected = selection.selected === entry.id
  return (
    <li className="entry-row" data-entry-id={entry.id} aria-current={selected ? 'true' : undefined} data-done={entry.resolved_by ? 'true' : undefined} data-read={reading && entry.owner.read ? 'true' : undefined}>
      <div className="entry-row-main">
        <div className="entry-row-head">
          <button type="button" className="entry-row-title" aria-haspopup="dialog" onClick={() => selection.open(entry.id)}>
            {headline ? plainText(headline) : titleOf(entry)}
          </button>
          <FocusBadges entry={entry} />
        </div>
        {headline ? <p className="entry-row-gist">{titleOf(entry)}</p> : summary && <p className="entry-row-gist">{summary}</p>}
        <div className="entry-row-meta">
          {view === 'activity' && <KindBadge kind={entry.kind} />}
          {entry.meta?.category && <span className="category">{entry.meta.category}</span>}
          {reading && entry.meta?.source && <span>{entry.meta.source}</span>}
          {view !== 'todos' && !hideProject && <Link to={`/projects/${entry.slug}`}>{entry.project_name}</Link>}
          <span>{writerName(entry.source)}</span>
          {entry.reply_to && <span className="row-note">reply</span>}
          {(entry.replies ?? 0) > 0 && <span className="row-note" title="Replies">{entry.replies} {entry.replies === 1 ? 'reply' : 'replies'}</span>}
          {repeats.length > 0 && <span className="row-note">+{repeats.length} {repeats.length === 1 ? 'repeat' : 'repeats'}</span>}
          {view === 'todos' || view === 'inbox' ? <Timestamp iso={entry.created_at} /> : <time dateTime={entry.created_at} title={new Date(entry.created_at).toLocaleString()}>{timeFormat.format(new Date(entry.created_at))}</time>}
          {reading && entry.meta?.link && <a href={entry.meta.link} target="_blank" rel="noopener noreferrer">Open <Icon name="external" /></a>}
        </div>
      </div>
      <div className="entry-row-actions">
        {entry.kind === 'todo' && <TodoState entry={entry} onChanged={onChanged} />}
        {asking && <button type="button" className="btn btn-small" disabled={owner.busy} onClick={() => void owner.act(entry.id, { handled: true }, 'Marked handled.')}>Handled</button>}
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

function ActivityRun({ items, hideProject, onTag, onChanged }: { items: Folded[]; hideProject: boolean; onTag: (tag: string) => void; onChanged: () => void }) {
  const [expanded, setExpanded] = useState(false)
  const [first, ...rest] = items
  if (!first) return null
  return (
    <>
      <EntryRow entry={first.entry} repeats={first.repeats} view="activity" hideProject={hideProject} onTag={onTag} onChanged={onChanged} />
      {rest.length > 0 && !expanded && (
        <li className="entry-run-more">
          <button type="button" className="link-button" onClick={() => setExpanded(true)}>
            +{rest.length} more from {writerName(first.entry.source)} on {first.entry.project_name}
          </button>
        </li>
      )}
      {expanded && rest.map((item) => <EntryRow key={item.entry.id} entry={item.entry} repeats={item.repeats} view="activity" hideProject={hideProject} onTag={onTag} onChanged={onChanged} />)}
    </>
  )
}

/** Marks everything the unread reading list shows as read, with one Undo. */
function MarkAllRead({ filter, onChanged }: { filter: EntryFilter; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const undo = useUndo()
  const run = async () => {
    setBusy(true)
    try {
      const result = await api.markAllRead(filter)
      if (result.action_id) undo(`Marked ${result.count} read.`, result.action_id)
      else toast('Nothing left to read.')
      onChanged()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  return <p className="mark-all"><button type="button" className="btn btn-small" disabled={busy} onClick={() => void run()}>{busy ? 'Marking…' : 'Mark all read'}</button> <span className="muted small">Marks every unread item shown here, across all pages. You can undo it.</span></p>
}

/**
 * One filterable, paged entry list. With fixedProject it lists that project
 * only (a project page), without a project filter or project links.
 */
export function EntriesView({ view, initialProject = '', initialQuery = '', initialTag = '', initialSource = '', fixedProject }: { view: ListView; initialProject?: string; initialQuery?: string; initialTag?: string; initialSource?: string; fixedProject?: string }) {
  const [filter, setFilter] = useState<EntryFilter>({
    project: fixedProject ?? initialProject, q: initialQuery, tag: initialTag, source: initialSource, status: view === 'todos' ? 'open' : '', reading: view === 'reading' ? 'unread' : '',
    // Routine bookkeeping stays out of the way unless asked for (or searched).
    hide_routine: initialQuery ? '' : '1',
  })
  const [loadingMore, setLoadingMore] = useState(false)
  // Phones fold the dropdowns and the routine toggle behind a Filters button; desktop always shows them.
  const [filtersOpen, setFiltersOpen] = useState(false)
  const effective: EntryFilter = {
    ...filter,
    project: fixedProject ?? filter.project ?? '',
    kind: view === 'todos' ? 'todo' : view === 'decisions' ? 'decision' : view === 'reading' ? '' : filter.kind ?? '',
    status: view === 'todos' ? filter.status ?? '' : '',
    reading: view === 'reading' ? filter.reading || 'unread' : '',
    // A search looks everywhere, routine entries included.
    hide_routine: view === 'todos' || view === 'reading' || filter.q?.trim() ? '' : filter.hide_routine ?? '',
  }
  const key = `entries:${view}:${JSON.stringify(effective)}`
  const table = useResource(() => api.listEntries(effective), key, LIVE)
  const projects = useResource(() => api.listProjects(), 'table-projects', 'project')
  const toast = useToast()
  const set = (field: keyof EntryFilter, value: string) => setFilter((current) => ({ ...current, [field]: value }))
  const routineToggle = view === 'activity' || view === 'decisions'
  // Folded filters set away from their defaults; the search box stays in view, so it is not counted.
  const activeFilters = [
    !fixedProject && filter.project,
    view === 'todos' && filter.status !== 'open',
    view === 'reading' && effective.reading !== 'unread',
    view === 'activity' && filter.kind,
    filter.source,
    filter.tag,
    routineToggle && !filter.hide_routine,
  ].filter(Boolean).length

  // Todos read best per project, most important first. Fold while the list
  // is still newest first so each head is the newest copy, then sort heads.
  const folded = useMemo(() => {
    const heads = foldRepeats(table.data?.entries ?? [])
    if (view !== 'todos') return heads
    return [...heads].sort((a, b) => a.entry.project_name.localeCompare(b.entry.project_name) || a.entry.slug.localeCompare(b.entry.slug) || (PRIORITY_ORDER[a.entry.meta?.priority ?? 'normal'] ?? 1) - (PRIORITY_ORDER[b.entry.meta?.priority ?? 'normal'] ?? 1))
  }, [table.data, view])
  // Todos group by project slug (names may repeat); other views by day. A
  // project page's todos are one group.
  const groups = useMemo(() => groupBy(folded, (item) => (view === 'todos' ? (fixedProject ? '' : item.entry.slug) : dayLabel(item.entry.created_at))), [folded, view, fixedProject])
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
    <div className="entries-view" data-filters={filtersOpen ? 'open' : undefined}>
      <div className="filters table-filters">
        <label><span className="visually-hidden">Search text</span><input type="search" maxLength={1000} placeholder="Search titles and text" value={filter.q ?? ''} onChange={(event) => set('q', event.target.value)} /></label>
        <button type="button" className="btn filter-toggle" aria-expanded={filtersOpen} onClick={() => setFiltersOpen((open) => !open)}>
          <Icon name="filter" /> Filters {activeFilters > 0 && <span className="count">{activeFilters}{' '}<span className="visually-hidden">active</span></span>}
        </button>
        {!fixedProject && <label className="filter-extra"><span className="visually-hidden">Filter by project</span><select value={filter.project ?? ''} onChange={(event) => set('project', event.target.value)}><option value="">Any project</option>{(projects.data ?? []).map((item) => <option key={item.slug} value={item.slug}>{projectLabel(item, projects.data ?? [])}</option>)}</select></label>}
        {view === 'todos' && <label className="filter-extra"><span className="visually-hidden">Filter by state</span><select value={filter.status ?? ''} onChange={(event) => set('status', event.target.value)}><option value="open">Open</option><option value="done">Done</option><option value="">Open and done</option></select></label>}
        {view === 'reading' && <label className="filter-extra"><span className="visually-hidden">Filter reading</span><select value={effective.reading} onChange={(event) => set('reading', event.target.value)}><option value="unread">Unread</option><option value="starred">Starred</option><option value="all">All</option></select></label>}
        {view === 'activity' && <label className="filter-extra"><span className="visually-hidden">Filter by kind</span><select value={filter.kind ?? ''} onChange={(event) => set('kind', event.target.value)}><option value="">Any kind</option>{ENTRY_KINDS.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>}
        <label className="filter-extra"><span className="visually-hidden">Filter by agent</span><select value={filter.source ?? ''} onChange={(event) => set('source', event.target.value)}><option value="">Any agent</option>{[...new Set([...(filter.source ? [filter.source] : []), ...(table.data?.sources ?? [])])].map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
        <label className="filter-extra"><span className="visually-hidden">Filter by tag</span><select value={filter.tag ?? ''} onChange={(event) => set('tag', event.target.value)}><option value="">Any tag</option>{[...new Set([...(filter.tag ? [filter.tag] : []), ...(table.data?.tags ?? [])])].map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      </div>
      {view === 'reading' && effective.reading === 'unread' && count > 0 && <MarkAllRead filter={effective} onChanged={refreshAll} />}
      {routineToggle && (
        <label className="check filter-extra"><input type="checkbox" checked={!filter.hide_routine} onChange={(event) => set('hide_routine', event.target.checked ? '' : '1')} /> Show routine entries</label>
      )}
      {/* A project page adds to its own project, whether or not the project list loaded. */}
      {view !== 'reading' && (fixedProject || (projects.data?.length ?? 0) > 0) && (
        <AddRow key={view} projects={fixedProject ? [{ slug: fixedProject, name: fixedProject }] : projects.data ?? []} initialProject={fixedProject ?? initialProject}
          defaultKind={view === 'todos' ? 'todo' : view === 'decisions' ? 'decision' : 'note'} fixed={Boolean(fixedProject)} onAdded={refreshAll} />
      )}
      {table.loading && <Loading label="Loading entries…" />}
      {!table.loading && !table.data && <ErrorState message="Couldn't load entries." onRetry={table.reload} />}
      {table.stale && <StaleNotice message="Table may be out of date." onRetry={table.reload} />}
      {table.data && count === 0 && <EmptyState><p>{empty}</p></EmptyState>}
      {groups.map((group) => (
        <section key={group.key} className="entry-group" aria-label={view === 'todos' ? group.items[0]?.entry.project_name ?? 'Todos' : group.key}>
          {group.key !== '' && (
            <h2 className="entry-group-title">
              {view === 'todos' ? group.items[0]?.entry.project_name : group.key} {view === 'todos' && <code className="muted">{group.key}</code>} <span className="count">{group.items.length}</span>
            </h2>
          )}
          <ul className="entry-list">
            {view === 'activity'
              ? groupBy(group.items, (item) => `${item.entry.slug}\u0000${item.entry.source}`).map((run) => <ActivityRun key={run.items[0]?.entry.id} items={run.items} hideProject={Boolean(fixedProject)} onTag={(tag) => set('tag', tag)} onChanged={refreshAll} />)
              : group.items.map((item) => <EntryRow key={item.entry.id} entry={item.entry} repeats={item.repeats} view={view} hideProject={Boolean(fixedProject)} onTag={(tag) => set('tag', tag)} onChanged={refreshAll} />)}
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
    </div>
  )
}

/**
 * A project's health label. Only "blocked" is shown: a latest status of "done"
 * means that update finished something, not that the project did.
 */
export function HealthBadge({ state }: { state: string }) {
  if (state !== 'blocked') return null
  return <span className="badge" data-state="blocked">Blocked</span>
}

/** Everything that needs the owner, most urgent first. */
export function InboxView() {
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
            {asks.map((entry) => <EntryRow key={entry.id} entry={entry} view="inbox" headline={entry.meta?.ask ?? titleOf(entry)} onTag={noop} onChanged={refreshAll} />)}
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
            {todos.map((entry) => <EntryRow key={entry.id} entry={entry} view="inbox" onTag={noop} onChanged={refreshAll} />)}
          </ul>
        )}
      </section>
      {blocked.length > 0 && (
        <section className="entry-group" aria-label="Blocked">
          <h2 className="entry-group-title">Blocked <span className="count">{blocked.length}</span></h2>
          <ul className="project-lines">
            {blocked.map((project) => (
              <li key={project.slug}>
                <Link to={`/projects/${encodeURIComponent(project.slug)}`}>{project.name}</Link>
                <span className="muted"> · {plainText(project.status_detail || project.status_title || project.status_body)}</span>
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
                <Link to={`/projects/${encodeURIComponent(project.slug)}`}>{project.name}</Link> <HealthBadge state={project.status_state} />
                <p className="clamp digest">{plainText(project.digest)}</p>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

/**
 * AI labelling status: progress while a labeller works, or why it is paused
 * while entries wait. Says nothing when there is no labeller at all.
 */
export function AiStatus() {
  const summaries = useResource(api.getProjectSummaries, 'ai-status', 'entry entry_meta')
  // A labeller that stops or loses its model sends no live event; check each minute.
  const { reload } = summaries
  useEffect(() => {
    const timer = window.setInterval(reload, 60_000)
    return () => window.clearInterval(timer)
  }, [reload])
  const progress = summaries.data?.metadata
  if (!progress) return null
  const waiting = progress.total - progress.ready - progress.failed
  if (waiting <= 0) return null
  if (progress.configured && (!progress.active || progress.problem)) {
    return (
      <p className="notice ai-paused" role="status">
        AI labelling is paused: {progress.problem || 'the labelling service is not running'}. {waiting} {waiting === 1 ? 'entry is' : 'entries are'} waiting and will get titles and labels when it is back; until then {waiting === 1 ? 'it shows its' : 'they show their'} first line.
      </p>
    )
  }
  if (!progress.active) return null
  return <p className="muted small" role="status">AI summaries: {progress.ready} of {progress.total} entries processed. New ones appear as they finish.</p>
}
