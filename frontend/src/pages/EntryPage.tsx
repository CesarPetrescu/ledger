import { api, type TableEntry } from '../api'
import { useResource } from '../hooks/useResource'
import { DeleteEntry, Facts, FocusBadges, LIVE, RelatedList, TodoState, titleOf, useOwnerAction } from '../components/entries'
import { LabelEditor } from '../components/LabelEditor'
import { Link, navigate } from '../router'
import { ErrorState, Icon, KindBadge, Loading, StaleNotice, Timestamp } from '../components/ui'

/** One entry on its own page: search results, related entries, and repeats link here. */
export function EntryPage({ id }: { id: string }) {
  const entry = useResource(() => api.getEntry(id), `entry:${id}`, LIVE)
  if (entry.loading) return <Loading label="Loading entry…" />
  if (!entry.data) {
    return entry.error === 'entry not found' ? (
      <div className="state state-empty">
        <p>This entry doesn't exist, or it was deleted. Deleted entries stay in Trash for 30 days.</p>
        <Link to="/table?view=trash" className="btn">Open Trash</Link>
      </div>
    ) : <ErrorState message="Couldn't load this entry." onRetry={entry.reload} />
  }
  const e = entry.data
  const meta = e.meta
  return (
    <article className="entry-page">
      <Link to={`/projects/${encodeURIComponent(e.slug)}`} className="back-link"><Icon name="back" /> {e.project_name}</Link>
      {entry.stale && <StaleNotice message="Showing the last loaded version; refresh failed." onRetry={entry.reload} />}
      <header className="entry-page-head">
        <p className="entry-page-context">
          <KindBadge kind={e.kind} /> <Link to={`/projects/${encodeURIComponent(e.slug)}`}>{e.project_name}</Link> · <code>{e.source}</code> · <Timestamp iso={e.created_at} />
        </p>
        <h1>{titleOf(e)}</h1>
        <FocusBadges entry={e} />
        {meta?.gist && <p className="lede">{meta.gist}</p>}
        {meta && meta.tags.length > 0 && <p className="entry-page-tags">{meta.tags.map((tag) => <Link key={tag} className="chip" to={`/table?view=activity&tag=${encodeURIComponent(tag)}`}>{tag}</Link>)}</p>}
      </header>
      <EntryActions entry={e} onChanged={entry.reload} />
      {e.duplicate_of && <p className="entry-page-note">This repeats <Link to={`/entries/${e.duplicate_of}`}>an earlier entry</Link> about the same thing.</p>}
      {e.resolved_by && (
        <p className="entry-page-note">
          Closed <Timestamp iso={e.resolved_by.created_at} /> by <Link to={`/entries/${e.resolved_by.entry_id}`}>{e.resolved_by.origin === 'model' ? "an agent's update (detected automatically)" : 'you'}</Link>.
        </p>
      )}
      <Facts entry={e} />
      <section className="entry-page-section" aria-labelledby="entry-text">
        <h2 id="entry-text" className="section-title">Full text</h2>
        <p className="entry-body">{e.body}</p>
        {meta && meta.refs.length > 0 && <ul className="refs" aria-label="References">{meta.refs.map((ref) => <li key={ref}><code>{ref}</code></li>)}</ul>}
      </section>
      <section className="entry-page-section" aria-labelledby="entry-labels">
        <h2 id="entry-labels" className="section-title">Labels</h2>
        <p className="muted small">{!meta ? 'The AI has not labelled this entry yet.' : meta.origin === 'model' ? 'Title, summary, and tags were written by AI from the text above. Correct anything it got wrong; similar entries will be labelled the same way.' : 'Written from the console.'}</p>
        <LabelEditor entry={e} onChanged={entry.reload} />
      </section>
      {e.repeats.length > 0 && (
        <section className="entry-page-section" aria-labelledby="entry-repeats">
          <h2 id="entry-repeats" className="section-title">Repeats <span className="count">{e.repeats.length}</span></h2>
          <ul className="repeats">
            {e.repeats.map((repeat) => <li key={repeat.id}><Link to={`/entries/${repeat.id}`}>{titleOf(repeat)}</Link> <span className="muted">· {repeat.source} · <Timestamp iso={repeat.created_at} /></span></li>)}
          </ul>
        </section>
      )}
      <RelatedList id={e.id} />
      {/* A deleted entry has no page; its project is where the undo toast makes sense. */}
      <p className="detail-links"><DeleteEntry entry={e} onChanged={() => navigate(`/projects/${encodeURIComponent(e.slug)}`)} /></p>
    </article>
  )
}

/** Every action that applies to this entry, not only the ones a list view shows. */
function EntryActions({ entry, onChanged }: { entry: TableEntry; onChanged: () => void }) {
  const owner = useOwnerAction(onChanged)
  const asking = Boolean(entry.meta?.ask && !entry.owner.handled)
  const link = entry.meta?.link
  if (entry.kind !== 'todo' && !asking && !link) return null
  return (
    <div className="entry-page-actions">
      {entry.kind === 'todo' && <TodoState entry={entry} onChanged={onChanged} />}
      {asking && <button type="button" className="btn btn-small" disabled={owner.busy} onClick={() => void owner.act(entry.id, { handled: true }, 'Marked handled.')}>Handled</button>}
      {(asking || (entry.kind === 'todo' && !entry.resolved_by)) && (
        <button type="button" className="link-button" disabled={owner.busy} onClick={() => void owner.act(entry.id, { snooze_days: 1 }, 'Snoozed until tomorrow.')}>Snooze until tomorrow</button>
      )}
      {link && (
        <>
          <a className="btn btn-small" href={link} target="_blank" rel="noopener noreferrer">Open link <Icon name="external" /></a>
          <button type="button" className="link-button" disabled={owner.busy} onClick={() => void owner.act(entry.id, { read: !entry.owner.read }, entry.owner.read ? 'Marked unread.' : 'Marked read.')}>
            {entry.owner.read ? 'Mark unread' : 'Mark read'}
          </button>
          <button type="button" className="star-button" aria-pressed={entry.owner.starred} aria-label={entry.owner.starred ? 'Unstar' : 'Star'} disabled={owner.busy}
            onClick={() => void owner.act(entry.id, { starred: !entry.owner.starred }, entry.owner.starred ? 'Unstarred.' : 'Starred.')}>
            {entry.owner.starred ? '★' : '☆'}
          </button>
        </>
      )}
    </div>
  )
}
