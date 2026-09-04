import { useMemo, useState, type FormEvent } from 'react'
import { api, ENTRY_KINDS, TIERS, type Entry, type Project, type ProjectInput } from '../api'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Icon, KindBadge, Loading, StaleNotice, TierBadge, Timestamp } from '../components/ui'
import { useResource } from '../hooks/useResource'
import { Link, navigate } from '../router'

const SLUG_PATTERN = '[a-z0-9][a-z0-9-]{1,63}'
const MAX_BODY = 4000

interface ProjectFormProps {
  mode: 'create' | 'edit'
  project?: Project
  onSaved: (project: Project) => void
  onCancel?: () => void
}

const EMPTY: ProjectInput = { name: '', tier: 'focus', hours_wk: 0, type: '', description: '', goal: '', deadline: '', needs_me: '', automate: '', stack: '' }

function editableProject(project: Project): ProjectInput {
  return {
    name: project.name,
    tier: project.tier,
    hours_wk: project.hours_wk,
    type: project.type,
    description: project.description,
    goal: project.goal,
    deadline: project.deadline,
    needs_me: project.needs_me,
    automate: project.automate,
    stack: project.stack,
  }
}

function ProjectForm({ mode, project, onSaved, onCancel }: ProjectFormProps) {
  const [slug, setSlug] = useState(project?.slug ?? '')
  const [input, setInput] = useState<ProjectInput>(project ? editableProject(project) : EMPTY)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const set = <K extends keyof ProjectInput>(key: K, value: ProjectInput[K]) => setInput((current) => ({ ...current, [key]: value }))

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      onSaved(await api.saveProject(slug.trim(), input))
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not save the project.')
    } finally {
      setBusy(false)
    }
  }

  const title = mode === 'create' ? 'New project' : 'Edit project'
  return (
    <form className="form" aria-label={title} onSubmit={(event) => void submit(event)}>
      <h2>{title}</h2>
      <div className="form-grid">
        {mode === 'create' && (
          <label>
            Slug
            <input value={slug} onChange={(event) => setSlug(event.target.value)} pattern={SLUG_PATTERN} required autoComplete="off" spellCheck={false} />
            <span className="hint">2 to 64 lowercase letters, digits, or hyphens. Permanent.</span>
          </label>
        )}
        <label>
          Name
          <input value={input.name} onChange={(event) => set('name', event.target.value)} required maxLength={200} />
        </label>
        <label>
          Tier
          <select value={input.tier} onChange={(event) => set('tier', event.target.value)}>
            {TIERS.map((tier) => (
              <option key={tier} value={tier}>
                {tier}
              </option>
            ))}
          </select>
        </label>
        <label>
          Hours per week
          <input type="number" min={0} max={168} value={input.hours_wk} onChange={(event) => set('hours_wk', Number(event.target.value))} required />
        </label>
        <label>
          Type
          <input value={input.type ?? ''} onChange={(event) => set('type', event.target.value)} />
        </label>
        <label>
          Deadline
          <input value={input.deadline ?? ''} onChange={(event) => set('deadline', event.target.value)} maxLength={200} />
        </label>
        <label className="span-2">
          Goal
          <input value={input.goal ?? ''} onChange={(event) => set('goal', event.target.value)} />
        </label>
        <label className="span-2">
          Description
          <textarea value={input.description ?? ''} onChange={(event) => set('description', event.target.value)} rows={3} />
        </label>
        <label>
          Needs me
          <input value={input.needs_me ?? ''} onChange={(event) => set('needs_me', event.target.value)} />
        </label>
        <label>
          Automate
          <input value={input.automate ?? ''} onChange={(event) => set('automate', event.target.value)} />
        </label>
        <label className="span-2">
          Stack
          <input value={input.stack ?? ''} onChange={(event) => set('stack', event.target.value)} />
        </label>
      </div>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <div className="form-actions">
        {onCancel && (
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        )}
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Saving…' : mode === 'create' ? 'Create project' : 'Save changes'}
        </button>
      </div>
    </form>
  )
}

function Composer({ slug, onAppended }: { slug: string; onAppended: (entry: Entry) => void }) {
  const [kind, setKind] = useState<string>('note')
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const ready = body.trim().length > 0 && body.length <= MAX_BODY

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!ready || busy) return
    setBusy(true)
    setError('')
    try {
      const entry = await api.appendEntry(slug, kind, body)
      onAppended(entry)
      setBody('')
      toast('Entry appended.')
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not append the entry.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="composer" aria-label="Append entry" onSubmit={(event) => void submit(event)}>
      <div className="composer-row">
        <label>
          Kind
          <select value={kind} onChange={(event) => setKind(event.target.value)}>
            {ENTRY_KINDS.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
        </label>
        <span className="muted small counter">
          {body.length}/{MAX_BODY}
        </span>
      </div>
      <label>
        Body
        <textarea value={body} onChange={(event) => setBody(event.target.value)} rows={4} maxLength={MAX_BODY} placeholder="What happened, what was decided, what is next…" />
      </label>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <div className="form-actions">
        <span className="muted small">Entries are permanent and attributed to this console session.</span>
        <button type="submit" className="btn btn-primary" disabled={!ready || busy}>
          {busy ? 'Appending…' : 'Append'}
        </button>
      </div>
    </form>
  )
}

const META_FIELDS: { key: keyof Project; label: string }[] = [
  { key: 'goal', label: 'Goal' },
  { key: 'deadline', label: 'Deadline' },
  { key: 'type', label: 'Type' },
  { key: 'description', label: 'Description' },
  { key: 'needs_me', label: 'Needs me' },
  { key: 'automate', label: 'Automate' },
  { key: 'stack', label: 'Stack' },
]

function ProjectDetail({ slug, onSaved, onEntryAppended }: { slug: string; onSaved: (project: Project) => void; onEntryAppended: (entry: Entry) => void }) {
  const detail = useResource(() => api.getProject(slug), `project:${slug}`, 'project entry')
  const [editing, setEditing] = useState(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [olderError, setOlderError] = useState('')
  const toast = useToast()

  const loadOlder = async () => {
    const before = detail.data?.next_before
    if (before === undefined || loadingOlder) return
    setLoadingOlder(true)
    setOlderError('')
    try {
      const page = await api.getProject(slug, before)
      detail.update((current) => {
        const updated: typeof current = { ...current, entries: [...current.entries, ...page.entries] }
        if (page.next_before === undefined) delete updated.next_before
        else updated.next_before = page.next_before
        return updated
      })
    } catch (failure) {
      setOlderError(failure instanceof Error ? failure.message : 'Could not load older entries.')
    } finally {
      setLoadingOlder(false)
    }
  }

  if (detail.loading) return <Loading label="Loading project…" />
  if (!detail.data) {
    const missing = detail.error === 'project not found'
    return <ErrorState message={missing ? 'Project not found.' : "Couldn't load this project."} onRetry={missing ? undefined : detail.reload} />
  }
  const { project, entries } = detail.data

  return (
    <article className="detail">
      <header className="detail-head">
        <Link to="/projects" className="back-link">
          <Icon name="back" /> Projects
        </Link>
        <div className="detail-title">
          <h1>{project.name}</h1>
          <div className="meta-row">
            <code>{project.slug}</code>
            <TierBadge tier={project.tier} />
            <span>{project.hours_wk} h/wk</span>
            <span className="muted">
              updated <Timestamp iso={project.updated_at} />
            </span>
          </div>
        </div>
        {!editing && (
          <button type="button" className="btn" onClick={() => setEditing(true)}>
            Edit project
          </button>
        )}
      </header>
      {detail.stale && <StaleNotice message="Showing the last loaded version; refresh failed." onRetry={detail.reload} />}
      {editing ? (
        <ProjectForm
          mode="edit"
          project={project}
          onCancel={() => setEditing(false)}
          onSaved={(saved) => {
            detail.update((current) => ({ ...current, project: saved }))
            setEditing(false)
            onSaved(saved)
            toast('Project saved.')
          }}
        />
      ) : (
        <ul className="meta-grid" aria-label="Project metadata">
          {META_FIELDS.map((field) => (
            <li key={field.key}>
              <span className="meta-label">{field.label}</span>
              <span className="meta-value">{String(project[field.key] ?? '') || <span className="muted">—</span>}</span>
            </li>
          ))}
        </ul>
      )}
      <Composer
        slug={project.slug}
        onAppended={(entry) => {
          detail.update((current) => ({ ...current, entries: [entry, ...current.entries] }))
          onEntryAppended(entry)
        }}
      />
      <section aria-labelledby="timeline-title" className="timeline-section">
        <h2 id="timeline-title" className="panel-title">
          Timeline <span className="count">{entries.length} loaded</span>
        </h2>
        {entries.length === 0 ? (
          <p className="muted">No entries yet.</p>
        ) : (
          <ol className="timeline">
            {entries.map((entry) => (
              <li key={entry.id}>
                <div className="entry-head">
                  <KindBadge kind={entry.kind} />
                  <Timestamp iso={entry.created_at} />
                  <code>{entry.source}</code>
                  <code className="muted">{entry.client_id}</code>
                  <code className="muted">#{entry.id}</code>
                </div>
                <p className="entry-body">{entry.body}</p>
              </li>
            ))}
          </ol>
        )}
        {detail.data.next_before !== undefined && (
          <button type="button" className="btn" disabled={loadingOlder} onClick={() => void loadOlder()}>
            {loadingOlder ? 'Loading…' : 'Load older entries'}
          </button>
        )}
        {olderError && (
          <p className="field-error" role="alert">
            {olderError}
          </p>
        )}
      </section>
    </article>
  )
}

export function ProjectsPage({ slug }: { slug?: string | undefined }) {
  const list = useResource(() => api.listProjects(), 'projects', 'project entry')
  const [filter, setFilter] = useState('')
  const [tier, setTier] = useState('all')
  const toast = useToast()

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return (list.data ?? []).filter((project) => (tier === 'all' || project.tier === tier) && (needle === '' || project.name.toLowerCase().includes(needle) || project.slug.includes(needle)))
  }, [list.data, filter, tier])

  const upsertInList = (saved: Project) =>
    list.update((projects) => {
      const existing = projects.find((project) => project.slug === saved.slug)
      const merged = existing && saved.last_entry_at === undefined && existing.last_entry_at !== undefined ? { ...saved, last_entry_at: existing.last_entry_at } : saved
      return [merged, ...projects.filter((project) => project.slug !== saved.slug)].sort((a, b) => a.slug.localeCompare(b.slug))
    })
  const updateLastEntry = (entry: Entry) =>
    list.update((projects) => projects.map((project) => (project.slug === entry.slug ? { ...project, last_entry_at: entry.created_at } : project)))
  const mode = slug ? 'detail' : 'list'

  return (
    <div className="split" data-mode={mode}>
      <section className="pane pane-list" aria-label="Project list">
        <header className="page-head">
          <h1>Projects</h1>
          <Link to="/projects/_new" className="btn btn-primary">
            <Icon name="plus" /> New project
          </Link>
        </header>
        <div className="filters">
          <label className="visually-hidden" htmlFor="project-filter">
            Filter projects
          </label>
          <input id="project-filter" type="search" placeholder="Name or slug" value={filter} onChange={(event) => setFilter(event.target.value)} autoComplete="off" />
          <fieldset className="segmented">
            <legend className="visually-hidden">Tier</legend>
            {['all', ...TIERS].map((option) => (
              <label key={option}>
                <input type="radio" name="tier" value={option} checked={tier === option} onChange={() => setTier(option)} />
                <span>{option === 'all' ? 'All' : option}</span>
              </label>
            ))}
          </fieldset>
        </div>
        {list.loading && <Loading label="Loading projects…" />}
        {!list.loading && !list.data && <ErrorState message="Couldn't load projects." onRetry={list.reload} />}
        {list.stale && <StaleNotice message="Project list may be out of date." onRetry={list.reload} />}
        {list.data && visible.length === 0 && <p className="muted">No projects match.</p>}
        {list.data && visible.length > 0 && (
          <ul className="project-list" aria-label="Projects">
            {visible.map((project) => (
              <li key={project.slug}>
                <Link to={`/projects/${project.slug}`} aria-current={project.slug === slug ? 'page' : undefined}>
                  <span className="project-name">{project.name}</span>
                  <code>{project.slug}</code>
                  <TierBadge tier={project.tier} />
                  <span className="muted small">{project.last_entry_at ? <Timestamp iso={project.last_entry_at} /> : 'no entries'}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="pane pane-detail" aria-label="Project inspector">
        {slug === '_new' ? (
          <>
            <Link to="/projects" className="back-link">
              <Icon name="back" /> Projects
            </Link>
            <ProjectForm
              mode="create"
              onSaved={(saved) => {
                upsertInList(saved)
                toast('Project saved.')
                navigate(`/projects/${saved.slug}`)
              }}
            />
          </>
        ) : slug ? (
          <ProjectDetail key={slug} slug={slug} onSaved={upsertInList} onEntryAppended={updateLastEntry} />
        ) : (
          <EmptyState>
            <p>Select a project to inspect its record and timeline.</p>
          </EmptyState>
        )}
      </section>
    </div>
  )
}
