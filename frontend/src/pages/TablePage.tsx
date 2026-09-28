import { useEffect, useState } from 'react'
import { api, describeError } from '../api'
import { useResource } from '../hooks/useResource'
import { LegendButton } from '../components/help'
import { refreshAll } from '../live'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { AiStatus, EntriesView, daysUntil, LIVE, type ListView } from '../components/entries'
import { Link, navigate, useLocation } from '../router'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Loading, Timestamp } from '../components/ui'

/** Recent quick actions, each undoable on its own. */
function RecentView() {
  const actions = useResource(api.listActions, 'actions', LIVE)
  const toast = useToast()
  const [busy, setBusy] = useState('')
  const undo = async (id: string) => {
    setBusy(id)
    try {
      await api.undoAction(id)
      toast('Undone.')
      refreshAll()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy('')
    }
  }
  if (actions.loading) return <Loading label="Loading recent actions…" />
  if (!actions.data) return <ErrorState message="Couldn't load recent actions." onRetry={actions.reload} />
  if (actions.data.length === 0) return <EmptyState><p>No actions in the last 7 days.</p></EmptyState>
  return (
    <section className="entry-group" aria-label="Recent actions">
      <p className="muted small">Your last week of quick actions, newest first. Undo any of them on its own.</p>
      <ul className="entry-list">
        {actions.data.map((action) => (
          <li key={action.id} className="entry-row" data-done={action.undone_at ? 'true' : undefined}>
            <div className="entry-row-main">
              <p className="entry-row-title">{action.label}</p>
              <div className="entry-row-meta">
                {action.project_slug && <span>{action.project_slug}</span>}
                <Timestamp iso={action.created_at} />
                {action.undone_at && <span>Undone <Timestamp iso={action.undone_at} /></span>}
              </div>
            </div>
            <div className="entry-row-actions">
              {action.undoable && <button type="button" className="btn btn-small" disabled={busy !== ''} onClick={() => void undo(action.id)}>Undo</button>}
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** Deleted projects and entries, restorable for 30 days. */
function TrashView() {
  const trash = useResource(api.listTrash, 'trash', LIVE)
  const toast = useToast()
  const [purging, setPurging] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const run = async (work: () => Promise<unknown>, message: string) => {
    setBusy(true)
    try {
      await work()
      toast(message)
      refreshAll()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  if (trash.loading) return <Loading label="Loading trash…" />
  if (!trash.data) return <ErrorState message="Couldn't load the trash." onRetry={trash.reload} />
  if (trash.data.length === 0) return <EmptyState><p>Trash is empty.</p></EmptyState>
  return (
    <section className="entry-group" aria-label="Trash">
      <p className="muted small">Deleted items are removed for good after 30 days.</p>
      <ul className="entry-list">
        {trash.data.map((item) => (
          <li key={item.id} className="entry-row">
            <div className="entry-row-main">
              <p className="entry-row-title">{item.kind === 'project' ? `Project: ${item.label}` : item.label}</p>
              <div className="entry-row-meta">
                <span>{item.kind === 'project' ? `${item.entry_count} ${item.entry_count === 1 ? 'entry' : 'entries'}` : item.project_slug}</span>
                <span>deleted <Timestamp iso={item.deleted_at} /></span>
                <span>gone in {daysUntil(item.purge_at)} days</span>
              </div>
            </div>
            <div className="entry-row-actions">
              <button type="button" className="btn btn-small" disabled={busy} onClick={() => void run(() => api.restoreTrash(item.id), 'Restored.')}>Restore</button>
              <button type="button" className="link-button danger" disabled={busy} onClick={() => setPurging(item.id)}>Delete forever</button>
            </div>
          </li>
        ))}
      </ul>
      <ConfirmDialog open={purging !== null} title="Delete forever?" confirmLabel="Delete forever" busy={busy}
        onConfirm={() => { const id = purging; setPurging(null); if (id) void run(() => api.purgeTrash(id), 'Deleted forever.') }} onCancel={() => setPurging(null)}>
        <p>This cannot be undone. The item and everything in it is removed permanently.</p>
      </ConfirmDialog>
    </section>
  )
}

const VIEWS: { id: ListView; label: string }[] = [
  { id: 'activity', label: 'Activity' },
  { id: 'todos', label: 'Todos' },
  { id: 'decisions', label: 'Decisions' },
  { id: 'reading', label: 'Reading' },
]
type View = ListView | 'recent' | 'trash'

/** Every project's entries in one filterable list; the Inbox and projects have their own pages. */
export function TablePage() {
  const { query } = useLocation()
  const requested = query.get('view')
  // The inbox and the project summary used to be views here.
  const moved = requested === 'inbox' ? '/' : requested === 'projects' ? '/projects' : null
  useEffect(() => {
    if (moved) navigate(moved, { replace: true })
  }, [moved])
  const view: View = VIEWS.some((item) => item.id === requested) || requested === 'recent' || requested === 'trash' ? (requested as View) : 'activity'
  const project = query.get('project') ?? ''
  const search = query.get('q') ?? ''
  const tag = query.get('tag') ?? ''
  const source = query.get('source') ?? ''

  return (
    <>
      <header className="page-head">
        <div>
          <p className="eyebrow">What your agents are doing</p>
          <h1>Table</h1>
        </div>
        <LegendButton />
        <p className="muted">Every project's entries in one list. Narrow it by project, agent, tag, or text, open any row, or download it as a spreadsheet.</p>
        <AiStatus />
        <nav className="page-links" aria-label="History">
          <Link to="/table?view=recent" aria-current={view === 'recent' ? 'page' : undefined}>Recent actions</Link>
          <Link to="/table?view=trash" aria-current={view === 'trash' ? 'page' : undefined}>Trash</Link>
        </nav>
      </header>
      <nav className="view-tabs" aria-label="Table views">
        {VIEWS.map((item) => (
          <Link key={item.id} to={`/table?view=${item.id}`} aria-current={view === item.id ? 'page' : undefined}>
            {item.label}
          </Link>
        ))}
      </nav>
      {view === 'recent' ? <RecentView />
        : view === 'trash' ? <TrashView />
        : <EntriesView key={`${view}:${project}:${search}:${tag}:${source}`} view={view} initialProject={project} initialQuery={search} initialTag={tag} initialSource={source} />}
    </>
  )
}
