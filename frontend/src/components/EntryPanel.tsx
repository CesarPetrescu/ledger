import { createContext, useContext, useEffect, useRef, useState, type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { api, describeError, OWNER_SOURCE, type HistoryEvent, type TableEntry } from '../api'
import { useResource } from '../hooks/useResource'
import { useUndo } from '../hooks/useUndo'
import { refreshAll } from '../live'
import { DeleteEntry, Facts, FocusBadges, LIVE, RelatedList, TodoState, titleOf, useOwnerAction } from './entries'
import { LegendButton } from './help'
import { LabelEditor } from './LabelEditor'
import { MarkdownText, plainText } from './Markdown'
import { Link, navigate, useLocation } from '../router'
import { useToast } from './Toast'
import { ErrorState, Icon, KindBadge, Loading, StaleNotice, Timestamp } from './ui'

// Every list opens an entry beside itself, like Handoffs: the list keeps its
// place, the URL says which entry is open (?entry=ID), and Back closes it.

interface Selection {
  selected: string
  open: (id: string) => void
}

const SelectionContext = createContext<Selection | null>(null)

/** Which entry the surrounding list has open; outside a split view rows open the entry page. */
export function useEntrySelection(): Selection {
  return useContext(SelectionContext) ?? { selected: '', open: (id) => navigate(`/entries/${encodeURIComponent(id)}`) }
}

/** The name a writer is shown by; the owner is "You". */
export function writerName(source: string): string {
  return source === OWNER_SOURCE ? 'You' : source
}

function isTyping(target: EventTarget | null): boolean {
  return target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
}

/** A list with the open entry beside it. ↑/↓ move through the list, Esc closes. */
export function EntrySplit({ children }: { children: ReactNode }) {
  const { path, query } = useLocation()
  const selected = query.get('entry') ?? ''
  const listRef = useRef<HTMLDivElement>(null)
  const withEntry = (id: string) => {
    const next = new URLSearchParams(query)
    if (id) next.set('entry', id)
    else next.delete('entry')
    const search = next.toString()
    return search ? `${path}?${search}` : path
  }
  // Moving between entries replaces the URL; opening from nothing adds a Back step.
  const open = (id: string) => navigate(withEntry(id), { replace: Boolean(selected) })
  // Closing replaces the address, so Back leaves the list instead of reopening the entry.
  const close = () => navigate(withEntry(''), { replace: true })

  useEffect(() => {
    if (!selected) return
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || isTyping(event.target)) return
      if (document.querySelector('dialog[open]')) return
      // An open ⋯ menu owns its keys: Escape closes the menu, not the entry.
      if (event.target instanceof Element && event.target.closest('[role="menu"]')) return
      if (event.key === 'Escape') {
        event.preventDefault()
        close()
        return
      }
      if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
      const rows = [...(listRef.current?.querySelectorAll<HTMLElement>('[data-entry-id]') ?? [])]
      const index = rows.findIndex((row) => row.dataset.entryId === selected)
      const next = rows[index + (event.key === 'ArrowDown' ? 1 : -1)]
      if (!next?.dataset.entryId) return
      event.preventDefault()
      open(next.dataset.entryId)
      // Not every environment scrolls (tests run without layout).
      next.scrollIntoView?.({ block: 'nearest' })
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  return (
    <SelectionContext.Provider value={{ selected, open }}>
      <div className="entry-split" data-open={selected ? 'true' : undefined}>
        <div className="entry-split-list" ref={listRef}>{children}</div>
        {selected && (
          <aside className="entry-panel" aria-label="Entry">
            <EntryDetail key={selected} id={selected} onClose={close} />
          </aside>
        )}
      </div>
    </SelectionContext.Provider>
  )
}

/** Everything about one entry: what it asks, your reply, its history, and every action. */
export function EntryDetail({ id, onClose }: { id: string; onClose?: () => void }) {
  const entry = useResource(() => api.getEntry(id), `entry:${id}`, LIVE)
  const headingRef = useRef<HTMLHeadingElement>(null)
  // Keyboard and screen-reader users land on the entry they opened.
  const loaded = Boolean(entry.data)
  const inPanel = Boolean(onClose)
  useEffect(() => { if (inPanel && loaded) headingRef.current?.focus({ preventScroll: true }) }, [inPanel, loaded])
  if (entry.loading) return <Loading label="Loading entry…" />
  if (!entry.data) {
    return (
      <div className="entry-detail">
        {onClose && <PanelBar id={id} onClose={onClose} />}
        {entry.error === 'entry not found' ? (
          <div className="state state-empty">
            <p>This entry doesn't exist, or it was deleted. Deleted entries stay in Trash for 30 days.</p>
            <Link to="/table?view=trash" className="btn">Open Trash</Link>
          </div>
        ) : <ErrorState message="Couldn't load this entry." onRetry={entry.reload} />}
      </div>
    )
  }
  const e = entry.data
  const meta = e.meta
  // Only an agent asks you something; your own entries never wait on you.
  const asking = Boolean(meta?.ask && !e.owner.handled && e.source !== OWNER_SOURCE)
  const project = `/projects/${encodeURIComponent(e.slug)}`
  return (
    <article className="entry-detail">
      {onClose && <PanelBar id={id} onClose={onClose} />}
      {entry.stale && <StaleNotice message="Showing the last loaded version; refresh failed." onRetry={entry.reload} />}
      <header className="entry-detail-head">
        <p className="entry-detail-context">
          <KindBadge kind={e.kind} /> <Link to={project}>{e.project_name}</Link> · <span>{writerName(e.source)}</span> · <Timestamp iso={e.created_at} />
        </p>
        <h2 ref={headingRef} tabIndex={-1}>{titleOf(e)}</h2>
        <p className="entry-detail-badges"><FocusBadges entry={e} /> <LegendButton /></p>
        {meta?.gist && <p className="lede">{plainText(meta.gist)}</p>}
        {e.reply_to && <p className="entry-detail-note">A reply to <Link to={`/entries/${e.reply_to}`}>an earlier entry</Link>.</p>}
      </header>

      {asking && (
        <section className="entry-question" aria-label="Question for you">
          <p className="eyebrow">{e.source} asks you</p>
          <p className="entry-question-text">{plainText(meta?.ask ?? '')}</p>
        </section>
      )}

      <EntryActions entry={e} onDeleted={() => (onClose ? onClose() : navigate(project))} />
      <ReplyBox entry={e} asking={asking} />

      {meta?.tags && meta.tags.length > 0 && (
        <p className="entry-detail-tags">{meta.tags.map((tag) => <Link key={tag} className="chip" to={`/table?view=activity&tag=${encodeURIComponent(tag)}`}>{tag}</Link>)}</p>
      )}
      <Facts entry={e} hideAsk={asking} />
      {e.duplicate_of && <p className="entry-detail-note">This repeats <Link to={`/entries/${e.duplicate_of}`}>an earlier entry</Link> about the same thing.</p>}
      {e.resolved_by && (
        <p className="entry-detail-note">
          Closed <Timestamp iso={e.resolved_by.created_at} /> by <Link to={`/entries/${e.resolved_by.entry_id}`}>{e.resolved_by.origin === 'model' ? "an agent's update (detected automatically)" : 'you'}</Link>.
        </p>
      )}

      <details className="entry-detail-section" open={!meta?.gist}>
        <summary>Full text</summary>
        <div className="entry-body"><MarkdownText text={e.body} /></div>
        {meta && meta.refs.length > 0 && <ul className="refs" aria-label="References">{meta.refs.map((ref) => <li key={ref}><code>{ref}</code></li>)}</ul>}
      </details>

      <History entry={e} />
      <RelatedList id={e.id} />

      <section className="entry-detail-section entry-detail-labels" aria-label="Labels">
        <p className="muted small">{!meta ? 'The AI has not labelled this entry yet.' : meta.origin === 'model' ? 'Title, summary, and tags were written by AI from the text. Correct anything it got wrong; similar entries will be labelled the same way.' : 'Written from the console.'}</p>
        <LabelEditor entry={e} onChanged={refreshAll} />
      </section>
    </article>
  )
}

function PanelBar({ id, onClose }: { id: string; onClose: () => void }) {
  return (
    <div className="entry-panel-bar">
      <button type="button" className="link-button" onClick={onClose}><Icon name="back" /> Back to list</button>
      <span className="muted small entry-panel-keys">↑ ↓ to move · Esc to close</span>
      <Link to={`/entries/${encodeURIComponent(id)}`} className="link-button">Open as page</Link>
      <button type="button" className="icon-button" aria-label="Close entry" onClick={onClose}><Icon name="close" /></button>
    </div>
  )
}

/** Every action that applies to this entry, not only the ones a list row shows. */
function EntryActions({ entry, onDeleted }: { entry: TableEntry; onDeleted: () => void }) {
  const owner = useOwnerAction(refreshAll)
  const asking = Boolean(entry.meta?.ask && !entry.owner.handled && entry.source !== OWNER_SOURCE)
  const link = entry.meta?.link
  const snoozable = asking || (entry.kind === 'todo' && !entry.resolved_by)
  const snoozed = entry.owner.snoozed_until
  return (
    <div className="entry-detail-actions">
      {entry.kind === 'todo' && <TodoState entry={entry} onChanged={refreshAll} />}
      {asking && <button type="button" className="btn btn-small" disabled={owner.busy} onClick={() => void owner.act(entry.id, { handled: true }, 'Marked handled.')}>Handled</button>}
      {entry.meta?.ask && entry.owner.handled && entry.source !== OWNER_SOURCE && <button type="button" className="btn btn-small" disabled={owner.busy} onClick={() => void owner.act(entry.id, { handled: false }, 'Back in Needs you.')}>Not handled</button>}
      {snoozable && !snoozed && (
        <label className="snooze-select">
          <span className="visually-hidden">Snooze</span>
          <select value="" disabled={owner.busy} onChange={(event) => {
            const days = Number(event.target.value)
            if (days) void owner.act(entry.id, { snooze_days: days }, days === 1 ? 'Snoozed until tomorrow.' : `Snoozed for ${days} days.`)
          }}>
            <option value="">Snooze…</option>
            <option value="1">Until tomorrow</option>
            <option value="3">For 3 days</option>
            <option value="7">For a week</option>
          </select>
        </label>
      )}
      {snoozed && (
        <span className="muted small">Snoozed until {new Date(`${snoozed}T12:00:00`).toLocaleDateString()} <button type="button" className="link-button" disabled={owner.busy} onClick={() => void owner.act(entry.id, { snooze_days: 0 }, 'Unsnoozed.')}>Wake now</button></span>
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
      <span className="entry-detail-actions-end"><DeleteEntry entry={entry} onChanged={() => { onDeleted(); refreshAll() }} /></span>
    </div>
  )
}

/** Your answer, saved under the entry where the agent reads it. */
function ReplyBox({ entry, asking }: { entry: TableEntry; asking: boolean }) {
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const undo = useUndo()
  const agent = entry.source === OWNER_SOURCE ? '' : entry.source
  const send = async (event?: FormEvent) => {
    event?.preventDefault()
    if (!body.trim()) {
      setError('Write a reply first.')
      return
    }
    setBusy(true)
    setError('')
    try {
      const result = await api.replyToEntry(entry.id, body)
      setBody('')
      if (result.action_id) undo('Reply saved; marked handled.', result.action_id)
      else toast('Reply saved.')
      refreshAll()
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }
  const onKeyDown = (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) void send()
  }
  return (
    <form className="reply-box" data-asking={asking ? 'true' : undefined} onSubmit={(event) => void send(event)}>
      <label htmlFor={`reply-${entry.id}`} className={asking ? 'reply-label' : 'visually-hidden'}>{asking ? `Answer ${agent}` : 'Reply'}</label>
      <textarea id={`reply-${entry.id}`} rows={asking ? 3 : 2} maxLength={4000} value={body} disabled={busy}
        placeholder={asking ? 'Type your answer…' : agent ? `Add a note or instruction for ${agent}…` : 'Add a note…'}
        onChange={(event) => setBody(event.target.value)} onKeyDown={onKeyDown} aria-describedby={`reply-hint-${entry.id}`} />
      <div className="reply-box-foot">
        <p id={`reply-hint-${entry.id}`} className="muted small">
          {agent ? `Saved under this entry; ${agent} sees it the next time it checks Ledger.` : 'Saved under this entry.'}
          {asking ? ' Sending marks the question handled.' : ''} Ctrl+Enter sends.
        </p>
        <button type="submit" className="btn btn-small btn-primary" disabled={busy}>{busy ? 'Sending…' : asking ? 'Send answer' : 'Reply'}</button>
      </div>
      {error && <p className="field-error" role="alert">{error}</p>}
    </form>
  )
}

function describe(event: HistoryEvent): ReactNode {
  const who = writerName(event.actor)
  const link = (label: string) => event.entry_id ? <Link to={`/entries/${event.entry_id}`}>{label}</Link> : label
  switch (event.kind) {
    case 'created': return <><strong>{who}</strong> wrote it{event.text && who !== 'You' ? <> through <em>{event.text}</em></> : null}</>
    case 'repeat': return <><strong>{who}</strong> {link('wrote it again')}</>
    case 'resolved': return <><strong>{who}</strong> reported it finished in {link('an update')}</>
    case 'action': return <><strong>You</strong>: {event.text.toLowerCase()}{event.undone ? <span className="muted"> (undone)</span> : null}</>
    case 'labels': return <><strong>You</strong> corrected {event.text.replaceAll('_', ' ')}</>
    case 'reply': return <><strong>{who}</strong> replied</>
  }
}

/** Where the entry came from and everything that happened to it since, oldest first. */
function History({ entry }: { entry: TableEntry }) {
  const history = useResource(() => api.entryHistory(entry.id), `history:${entry.id}`, LIVE)
  return (
    <section className="entry-detail-section" aria-labelledby={`history-${entry.id}`}>
      <h3 id={`history-${entry.id}`} className="section-title">History</h3>
      {entry.context && <p className="entry-origin"><span className="muted">Written from</span> {entry.context}</p>}
      {history.loading ? <Loading label="Loading history…" /> : !history.data ? (
        <ErrorState message="Couldn't load the history." onRetry={history.reload} />
      ) : (
        <ol className="timeline">
          {history.data.truncated && <li className="timeline-more muted small">Older history is not shown; this lists the newest 200 events.</li>}
          {/* Replying already shows as the reply; its "marked handled" twin would repeat it. */}
          {history.data.history.filter((event) => !(event.kind === 'action' && event.text.startsWith('Replied'))).map((event, index) => (
            <li key={index} data-kind={event.kind} data-owner={event.actor === OWNER_SOURCE ? 'true' : undefined}>
              <p className="timeline-line">{describe(event)} <Timestamp iso={event.at} /></p>
              {event.kind === 'reply' && <p className="timeline-message">{event.text}</p>}
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}
