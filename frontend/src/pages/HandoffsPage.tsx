import { useMemo, useRef, useState, type FormEvent } from 'react'
import {
  api,
  describeError,
  type Handoff,
  type HandoffCreateInput,
  type HandoffFile,
  type HandoffMessage,
  type HandoffWorkState,
  type Project,
} from '../api'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Icon, Loading, StaleNotice, Timestamp } from '../components/ui'
import { useResource } from '../hooks/useResource'
import { Link, navigate } from '../router'

const MAX_FILES = 10
const MAX_FILE_BYTES = 25 * 1024 * 1024
const MAX_MESSAGE_BYTES = 100 * 1024 * 1024
const WORK_STATES: { value: '' | HandoffWorkState; label: string }[] = [
  { value: '', label: 'Any status' },
  { value: 'draft', label: 'Draft' },
  { value: 'ready', label: 'Ready' },
  { value: 'in_progress', label: 'In progress' },
  { value: 'blocked', label: 'Blocked' },
  { value: 'done', label: 'Done' },
]

function titleCase(value: string): string {
  return value.replaceAll('_', ' ').replace(/^./, (letter) => letter.toUpperCase())
}

function validateFiles(files: File[], existing: HandoffFile[] = []): string {
  if (files.length + existing.length > MAX_FILES) return 'Choose no more than 10 files.'
  if (files.some((file) => file.size === 0 || file.size > MAX_FILE_BYTES)) return 'Each file must be between 1 byte and 25 MiB.'
  if (files.reduce((total, file) => total + file.size, existing.reduce((total, file) => total + file.size_bytes, 0)) > MAX_MESSAGE_BYTES) return 'Files for one message cannot exceed 100 MiB.'
  return ''
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

function HandoffBadge({ value, kind }: { value: string; kind: 'delivery' | 'work' }) {
  return <span className="badge handoff-badge" data-status={value} data-kind={kind}>{titleCase(value)}</span>
}

function FileList({ files, removable = false, onRemoved }: { files: HandoffFile[]; removable?: boolean; onRemoved?: (id: string) => void }) {
  const [deleting, setDeleting] = useState('')
  const toast = useToast()
  const remove = async (file: HandoffFile) => {
    setDeleting(file.id)
    try {
      await api.deleteHandoffFile(file.id)
      onRemoved?.(file.id)
      toast('File removed.')
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setDeleting('')
    }
  }
  return (
    <ul className="handoff-files" aria-label="Attachments">
      {files.map((file) => (
        <li key={file.id}>
          <Icon name="paperclip" />
          <a href={`/admin/api/handoff-files/${file.id}`}>{file.filename}</a>
          <span className="muted small">{formatBytes(file.size_bytes)}</span>
          {removable && (
            <button type="button" className="link-button danger" disabled={deleting === file.id} onClick={() => void remove(file)}>
              Remove
            </button>
          )}
        </li>
      ))}
    </ul>
  )
}

async function attachAndPublish(message: HandoffMessage, files: File[]): Promise<HandoffMessage> {
  const attached = []
  for (const file of files) attached.push(await api.uploadHandoffFile(message.id, file))
  if (files.length === 0) return message
  return { ...await api.updateHandoffMessage(message.id, 'publish'), files: [...message.files, ...attached] }
}

function DraftUploader({ message, onUpdated }: { message: HandoffMessage; onUpdated: (message: HandoffMessage) => void }) {
  const [files, setFiles] = useState<File[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const fileError = validateFiles(files, message.files)
    if (files.length === 0 || busy || fileError) {
      setError(fileError)
      return
    }
    setBusy(true)
    setError('')
    try {
      onUpdated(await attachAndPublish(message, files))
      toast('Files uploaded and handoff published.')
    } catch (failure) {
      setError(`${describeError(failure)} The message remains a Draft.`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form className="draft-upload" onSubmit={(event) => void submit(event)}>
      <label>Add files<input type="file" multiple onChange={(event) => setFiles(Array.from(event.target.files ?? []))} /></label>
      <button type="submit" className="btn btn-primary" disabled={busy || files.length === 0}>{busy ? 'Uploading…' : 'Upload and publish'}</button>
      {error && <p className="field-error" role="alert">{error}</p>}
    </form>
  )
}

function NewHandoff({ projects, initialProject }: { projects: Project[]; initialProject: string }) {
  const [input, setInput] = useState<HandoffCreateInput>({ title: '', description: '', scope: '', project_slug: initialProject, target: '', body: '', draft: false })
  const [files, setFiles] = useState<File[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const set = <K extends keyof HandoffCreateInput>(key: K, value: HandoffCreateInput[K]) => setInput((current) => ({ ...current, [key]: value }))

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy) return
    const fileError = validateFiles(files)
    if (fileError) {
      setError(fileError)
      return
    }
    setBusy(true)
    setError('')
    let createdID = ''
    try {
      const detail = await api.createHandoff({ ...input, draft: files.length > 0 })
      createdID = detail.handoff.id
      const message = detail.messages[0]
      if (!message) throw new Error('The handoff was created without a message.')
      await attachAndPublish(message, files)
      toast('Handoff ready.')
      navigate(`/handoffs/${detail.handoff.id}`)
    } catch (failure) {
      if (createdID) {
        toast(`Upload failed; handoff saved as Draft. ${describeError(failure)}`, 'error')
        navigate(`/handoffs/${createdID}`)
      } else {
        setError(describeError(failure))
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="detail">
      <Link to="/handoffs" className="back-link"><Icon name="back" /> Handoffs</Link>
      <form className="form" aria-label="New handoff" onSubmit={(event) => void submit(event)}>
        <div>
          <p className="eyebrow">Agent connector</p>
          <h1>New handoff</h1>
        </div>
        <div className="form-grid">
          <label>Title<input required maxLength={200} value={input.title} onChange={(event) => set('title', event.target.value)} /></label>
          <label>Project<select value={input.project_slug} onChange={(event) => set('project_slug', event.target.value)}><option value="">No project</option>{projects.map((project) => <option key={project.slug} value={project.slug}>{project.name}</option>)}</select></label>
          <label className="span-2">Description<textarea maxLength={2000} rows={3} value={input.description} onChange={(event) => set('description', event.target.value)} /></label>
          <label className="span-2">Work scope<textarea maxLength={500} rows={3} value={input.scope} onChange={(event) => set('scope', event.target.value)} /></label>
          <label>Target<input maxLength={100} value={input.target} onChange={(event) => set('target', event.target.value)} placeholder="Optional agent or model" /></label>
          <label>Files<input type="file" multiple onChange={(event) => setFiles(Array.from(event.target.files ?? []))} /></label>
          <label className="span-2">Message<textarea required maxLength={100000} rows={9} value={input.body} onChange={(event) => set('body', event.target.value)} /></label>
        </div>
        {files.length > 0 && <p className="muted small">{files.length} file{files.length === 1 ? '' : 's'} · {formatBytes(files.reduce((total, file) => total + file.size, 0))}</p>}
        {error && <p className="field-error" role="alert">{error}</p>}
        <div className="form-actions">
          <Link to="/handoffs" className="btn">Cancel</Link>
          <button type="submit" className="btn btn-primary" disabled={busy}>{busy ? 'Creating…' : 'Create handoff'}</button>
        </div>
      </form>
    </div>
  )
}

function MessageComposer({ handoffID, onAppended }: { handoffID: string; onAppended: (message: HandoffMessage) => void }) {
  const [body, setBody] = useState('')
  const [target, setTarget] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const fileInput = useRef<HTMLInputElement>(null)
  const toast = useToast()

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const fileError = validateFiles(files)
    if (!body.trim() || busy || fileError) {
      setError(fileError)
      return
    }
    setBusy(true)
    setError('')
    try {
      const created = await api.appendHandoffMessage(handoffID, { body, target, draft: files.length > 0 })
      onAppended(created)
      const published = await attachAndPublish(created, files)
      if (files.length > 0) onAppended(published)
      setBody('')
      setTarget('')
      setFiles([])
      if (fileInput.current) fileInput.current.value = ''
      toast('Message appended.')
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="composer" aria-label="Append handoff message" onSubmit={(event) => void submit(event)}>
      <h2>Continue this handoff</h2>
      <div className="form-grid">
        <label>Target<input maxLength={100} value={target} onChange={(event) => setTarget(event.target.value)} placeholder="Optional agent or model" /></label>
        <label>Files<input ref={fileInput} type="file" multiple onChange={(event) => setFiles(Array.from(event.target.files ?? []))} /></label>
        <label className="span-2">Message<textarea required maxLength={100000} rows={6} value={body} onChange={(event) => setBody(event.target.value)} /></label>
      </div>
      {error && <p className="field-error" role="alert">{error}</p>}
      <div className="form-actions"><span className="muted small">Messages are permanent. Add a correction instead of editing.</span><button className="btn btn-primary" disabled={busy || !body.trim()}>{busy ? 'Appending…' : 'Append message'}</button></div>
    </form>
  )
}

function actionNames(message: HandoffMessage): string[] {
  const actions: string[] = []
  if (message.delivery_state === 'unseen' && message.work_state !== 'draft') actions.push('acknowledge')
  if (message.work_state === 'draft') actions.push('publish')
  if (message.work_state === 'ready') actions.push('claim')
  if (message.work_state === 'in_progress') actions.push('block', 'complete', 'release')
  if (message.work_state === 'blocked') actions.push('complete', 'release')
  if (message.work_state === 'done') actions.push('reopen')
  return actions
}

function HandoffThread({ id }: { id: string }) {
  const detail = useResource(() => api.getHandoff(id), `handoff:${id}`, 'handoff handoff_message handoff_file')
  const [busy, setBusy] = useState('')
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [clipboard] = useState(() => navigator.clipboard)
  const toast = useToast()

  const copy = async (text: string, label: string) => {
    try {
      await clipboard.writeText(text)
      toast(`${label} copied.`)
    } catch {
      toast('Clipboard access failed.', 'error')
    }
  }
  const act = async (message: HandoffMessage, action: string) => {
    setBusy(`${message.id}:${action}`)
    try {
      const updated = await api.updateHandoffMessage(message.id, action)
      detail.update((current) => ({ ...current, messages: current.messages.map((item) => item.id === updated.id ? updated : item) }))
      toast(`${titleCase(action)} complete.`)
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy('')
    }
  }
  const copyFull = async () => {
    try {
      await copy(await api.exportHandoff(id), 'Full handoff')
    } catch (failure) {
      toast(describeError(failure), 'error')
    }
  }
  const loadOlder = async () => {
    if (!detail.data?.next_before || loadingOlder) return
    setLoadingOlder(true)
    try {
      const page = await api.getHandoff(id, detail.data.next_before)
      detail.update((current) => {
        const updated = { ...current, messages: [...page.messages, ...current.messages] }
        if (page.next_before === undefined) delete updated.next_before
        else updated.next_before = page.next_before
        return updated
      })
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setLoadingOlder(false)
    }
  }

  if (detail.loading) return <Loading label="Loading handoff…" />
  if (!detail.data) return <ErrorState message={detail.error === 'handoff item not found' ? 'Handoff not found.' : "Couldn't load this handoff."} onRetry={detail.reload} />
  const { handoff, messages } = detail.data
  return (
    <article className="detail handoff-detail">
      <header className="detail-head">
        <Link to="/handoffs" className="back-link"><Icon name="back" /> Handoffs</Link>
        <div className="detail-title">
          <p className="eyebrow">{handoff.project_name || 'General handoff'}</p>
          <h1>{handoff.title}</h1>
          <div className="meta-row"><span>{handoff.source}</span><span>created <Timestamp iso={handoff.created_at} /></span><span>updated <Timestamp iso={handoff.updated_at} /></span>{handoff.archived_at && <span className="badge">Archived</span>}</div>
        </div>
        <button type="button" className="btn" onClick={() => void copyFull()}><Icon name="copy" /> Copy full handoff</button>
      </header>
      {detail.stale && <StaleNotice message="Showing the last loaded version; refresh failed." onRetry={detail.reload} />}
      <dl className="handoff-summary"><div><dt>Description</dt><dd>{handoff.description}</dd></div><div><dt>Work scope</dt><dd>{handoff.scope}</dd></div></dl>
      <section className="handoff-thread" aria-label="Handoff messages">
        <div className="section-head"><h2 className="section-title">Messages <span className="count">{messages.length} loaded</span></h2>{detail.data.next_before && <button type="button" className="btn" disabled={loadingOlder} onClick={() => void loadOlder()}>{loadingOlder ? 'Loading…' : 'Load older'}</button>}</div>
        <ol>
          {messages.map((message) => (
            <li key={message.id} className="handoff-message">
              <header>
                <div className="handoff-message-meta"><strong>{message.source}</strong><Timestamp iso={message.created_at} />{message.target && <span>to {message.target}</span>}</div>
                <div className="handoff-message-states"><HandoffBadge value={message.delivery_state} kind="delivery" /><HandoffBadge value={message.work_state} kind="work" /></div>
              </header>
              <p className="handoff-body">{message.body}</p>
              {message.files.length > 0 && <FileList files={message.files} removable={message.work_state === 'draft'} onRemoved={(fileID) => detail.update((current) => ({ ...current, messages: current.messages.map((item) => item.id === message.id ? { ...item, files: item.files.filter((file) => file.id !== fileID) } : item) }))} />}
              {message.work_state === 'draft' && <DraftUploader message={message} onUpdated={(updated) => detail.update((current) => ({ ...current, messages: current.messages.map((item) => item.id === updated.id ? updated : item) }))} />}
              <footer className="message-actions">
                <button type="button" className="btn btn-quiet" aria-label="Copy message" onClick={() => void copy(message.body, 'Message')}><Icon name="copy" /> Copy</button>
                {actionNames(message).map((action) => <button key={action} type="button" className={action === 'claim' || action === 'publish' ? 'btn btn-primary' : 'btn'} disabled={busy !== ''} onClick={() => void act(message, action)}>{busy === `${message.id}:${action}` ? 'Working…' : titleCase(action)}</button>)}
              </footer>
            </li>
          ))}
        </ol>
      </section>
      <MessageComposer handoffID={id} onAppended={(message) => detail.update((current) => ({ ...current, messages: current.messages.some((item) => item.id === message.id) ? current.messages.map((item) => item.id === message.id ? message : item) : [...current.messages, message] }))} />
    </article>
  )
}

function counts(handoff: Handoff): { label: string; value: number }[] {
  return [
    { label: 'Draft', value: handoff.draft_count },
    { label: 'Ready', value: handoff.ready_count },
    { label: 'In progress', value: handoff.in_progress_count },
    { label: 'Blocked', value: handoff.blocked_count },
    { label: 'Done', value: handoff.done_count },
  ].filter((item) => item.value > 0)
}

export function HandoffsPage({ id, creating = false, initialProject = '' }: { id?: string; creating?: boolean; initialProject?: string }) {
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState('')
  const [archive, setArchive] = useState('active')
  const [project, setProject] = useState('')
  const [target, setTarget] = useState('')
  const [loadingMore, setLoadingMore] = useState(false)
  const list = useResource(() => api.listHandoffs({ q: query, status, archive, project, target }), `handoffs:${query}:${status}:${archive}:${project}:${target}`, 'handoff handoff_message handoff_file')
  const projects = useResource(() => api.listProjects(), 'handoff-projects', 'project')
  const toast = useToast()
  const visible = useMemo(() => list.data?.handoffs ?? [], [list.data])
  const mode = id || creating ? 'detail' : 'list'

  const loadMore = async () => {
    if (!list.data?.next_before || loadingMore) return
    setLoadingMore(true)
    try {
      const page = await api.listHandoffs({ q: query, status, archive, project, target, before: list.data.next_before })
      list.update((current) => ({ handoffs: [...current.handoffs, ...page.handoffs], ...(page.next_before ? { next_before: page.next_before } : {}) }))
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <div className="split handoffs" data-mode={mode}>
      <section className="pane pane-list" aria-label="Handoff inbox">
        <header className="page-head"><h1>Handoffs</h1><Link to="/handoffs/new" className="btn btn-primary"><Icon name="plus" /> New</Link></header>
        <div className="filters">
          <label className="visually-hidden" htmlFor="handoff-search">Search handoffs</label>
          <input id="handoff-search" type="search" placeholder="Search title, scope, messages" value={query} onChange={(event) => setQuery(event.target.value)} />
          <div className="handoff-filter-row">
            <label><span className="visually-hidden">Work status</span><select value={status} onChange={(event) => setStatus(event.target.value)}>{WORK_STATES.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
            <label><span className="visually-hidden">Archive</span><select value={archive} onChange={(event) => setArchive(event.target.value)}><option value="active">Active</option><option value="archived">Archived</option><option value="all">All</option></select></label>
          </div>
          <div className="handoff-filter-row">
            <label><span className="visually-hidden">Filter by project</span><select value={project} onChange={(event) => setProject(event.target.value)}><option value="">Any project</option>{(projects.data ?? []).map((item) => <option key={item.slug} value={item.slug}>{item.name}</option>)}</select></label>
            <label><span className="visually-hidden">Filter by target</span><input value={target} maxLength={100} onChange={(event) => setTarget(event.target.value)} placeholder="Any target" /></label>
          </div>
        </div>
        {list.loading && <Loading label="Loading handoffs…" />}
        {!list.loading && !list.data && <ErrorState message="Couldn't load handoffs." onRetry={list.reload} />}
        {list.stale && <StaleNotice message="Handoff list may be out of date." onRetry={list.reload} />}
        {list.data && visible.length === 0 && <p className="muted">No handoffs match.</p>}
        {visible.length > 0 && <ul className="handoff-list" aria-label="Handoffs">{visible.map((handoff) => <li key={handoff.id}><Link to={`/handoffs/${handoff.id}`} aria-current={handoff.id === id ? 'page' : undefined}><strong>{handoff.title}</strong><span className="clamp">{handoff.description}</span><span className="handoff-list-meta">{handoff.project_name || 'General'} · <Timestamp iso={handoff.updated_at} /></span><span className="handoff-counts">{counts(handoff).map((item) => <span key={item.label} data-status={item.label.toLowerCase().replace(' ', '_')}>{item.value} {item.label}</span>)}</span></Link></li>)}</ul>}
        {list.data?.next_before && <button type="button" className="btn" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Load more handoffs'}</button>}
      </section>
      <section className="pane pane-detail" aria-label="Handoff inspector">
        {creating ? projects.loading ? <Loading label="Loading projects…" /> : <NewHandoff projects={projects.data ?? []} initialProject={initialProject} /> : id ? <HandoffThread key={id} id={id} /> : <EmptyState><p>Select a handoff or create one for another agent.</p></EmptyState>}
      </section>
    </div>
  )
}
