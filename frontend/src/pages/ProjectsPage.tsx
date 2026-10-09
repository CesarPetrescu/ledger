import { useMemo, useState, type FormEvent, useLayoutEffect, useRef } from 'react'
import { api, describeError, TIERS, type DeletionPreview, type Project, type ProjectInput, type ProjectRepo, type ProjectSummary, type RepoLinkInput } from '../api'
import { useToast } from '../components/Toast'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { OverflowMenu } from '../components/OverflowMenu'
import { useUndo } from '../hooks/useUndo'
import { EmptyState, ErrorState, Icon, Loading, ResearchStatusBadge, StaleNotice, TierBadge, Timestamp } from '../components/ui'
import { EntriesView, HealthBadge, LIVE, ProjectSummaryTable } from '../components/entries'
import { EntrySplit, writerName } from '../components/EntryPanel'
import { useResource } from '../hooks/useResource'
import { Link, navigate } from '../router'

const SLUG_PATTERN = '[a-z0-9][a-z0-9-]{1,63}'

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

const META_FIELDS: { key: keyof Project; label: string }[] = [
  { key: 'goal', label: 'Goal' },
  { key: 'deadline', label: 'Deadline' },
  { key: 'type', label: 'Type' },
  { key: 'description', label: 'Description' },
  { key: 'needs_me', label: 'Needs me' },
  { key: 'automate', label: 'Automate' },
  { key: 'stack', label: 'Stack' },
]

export type ProjectView = 'activity' | 'todos' | 'decisions' | 'details' | 'handoffs' | 'files' | 'repos'

function ProjectHandoffs({ slug }: { slug: string }) {
  const handoffs = useResource(() => api.listHandoffs({ project: slug, archive: 'all' }), `project-handoffs:${slug}`, 'handoff handoff_message')
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const loadMore = async () => {
    if (!handoffs.data?.next_before || loadingMore) return
    setLoadingMore(true)
    setError('')
    try {
      const page = await api.listHandoffs({ project: slug, archive: 'all', before: handoffs.data.next_before })
      handoffs.update((current) => ({ handoffs: [...current.handoffs, ...page.handoffs], ...(page.next_before ? { next_before: page.next_before } : {}) }))
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not load more handoffs.')
    } finally {
      setLoadingMore(false)
    }
  }
  if (handoffs.loading) return <Loading label="Loading handoffs…" />
  if (!handoffs.data) return <ErrorState message="Couldn't load project handoffs." onRetry={handoffs.reload} />
  return (
    <section aria-label="Project handoffs" className="project-related">
      <div className="section-head"><h2 className="section-title">Handoffs</h2><Link className="btn btn-primary" to={`/handoffs/new?project=${encodeURIComponent(slug)}`}><Icon name="plus" /> New handoff</Link></div>
      {handoffs.data.handoffs.length === 0 ? <p className="muted">No handoffs are linked to this project.</p> : (
        <ul>
          {handoffs.data.handoffs.map((handoff) => (
            <li key={handoff.id}>
              <Link to={`/handoffs/${handoff.id}`}>
                <strong>{handoff.kind === 'research' && <span className="badge research-badge">Research</span>}{handoff.title}</strong>
                {handoff.description && <span>{handoff.description}</span>}
                <span className="muted small project-handoff-meta">{handoff.research_status && <ResearchStatusBadge status={handoff.research_status} />}<span>Updated <Timestamp iso={handoff.updated_at} /></span></span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      {handoffs.data.next_before && <button type="button" className="btn" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Load more handoffs'}</button>}
      {error && <p className="field-error" role="alert">{error}</p>}
    </section>
  )
}

function ProjectFiles({ slug }: { slug: string }) {
  const files = useResource(() => api.listProjectFiles(slug), `project-files:${slug}`, 'handoff_file')
  if (files.loading) return <Loading label="Loading files…" />
  if (!files.data) return <ErrorState message="Couldn't load project files." onRetry={files.reload} />
  return (
    <section aria-label="Project files" className="project-related">
      <h2 className="section-title">Files from handoffs</h2>
      {files.data.length === 0 ? <p className="muted">No handoff files are linked to this project.</p> : <ul>{files.data.map((file) => <li key={file.id}><a href={`/admin/api/handoff-files/${file.id}`}><strong>{file.filename}</strong><span>{file.handoff_title}</span><span className="muted small">{file.size_bytes.toLocaleString()} bytes · <Timestamp iso={file.created_at} /></span></a></li>)}</ul>}
    </section>
  )
}

const EMPTY_REPO: RepoLinkInput = { url: '', role: '', branch: '', path: '', note: '' }

/** What the GitHub sync last saw, in one line. Synced text is external data, shown as plain text. */
function repoActivity(repo: ProjectRepo) {
  const sync = repo.sync
  if (!sync) return repo.provider === 'github' ? <span className="muted small">Not synced yet</span> : null
  // After a failed check the last good snapshot stays, shown under the error.
  const known = !sync.error || sync.head_at !== undefined || sync.open_prs !== undefined || Boolean(sync.latest_release)
  return (
    <>
      {sync.error && <span className="small repo-sync-error">Sync: {sync.error}</span>}
      {known && (
        <span className="muted small">
          {sync.head_at ? <>Last commit <Timestamp iso={sync.head_at} />{sync.head_message ? `: ${sync.head_message}` : ''}</> : 'No commits yet'}
          {sync.open_prs !== undefined && ` · ${sync.open_prs >= 100 ? '100+' : sync.open_prs} open PR${sync.open_prs === 1 ? '' : 's'}`}
          {sync.latest_release && ` · release ${sync.latest_release}`}
          {sync.archived && ' · archived'}
        </span>
      )}
    </>
  )
}

/** The Git repositories a project spans. Agents see them with the project and clone with their own access. */
function ProjectRepos({ slug }: { slug: string }) {
  const repos = useResource(() => api.listProjectRepos(slug), `project-repos:${slug}`, 'project_repo')
  const [input, setInput] = useState<RepoLinkInput>(EMPTY_REPO)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [target, setTarget] = useState<ProjectRepo | null>(null)
  const toast = useToast()
  const set = (key: keyof RepoLinkInput, value: string) => setInput((current) => ({ ...current, [key]: value }))

  const link = async (event: FormEvent) => {
    event.preventDefault()
    if (busy || !input.url.trim()) return
    setBusy(true)
    setError('')
    try {
      const repo = await api.linkProjectRepo(slug, input)
      setInput(EMPTY_REPO)
      // A live refresh may already have brought it in.
      repos.update((current) => current.some((item) => item.id === repo.id) ? current : [...current, repo])
      toast(`Linked ${repo.repo}.`)
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }

  const unlink = async () => {
    if (!target) return
    setBusy(true)
    try {
      await api.unlinkRepo(target.id)
      repos.update((current) => current.filter((repo) => repo.id !== target.id))
      toast(`Unlinked ${target.repo}.`)
      setTarget(null)
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }

  if (repos.loading) return <Loading label="Loading repositories…" />
  if (!repos.data) return <ErrorState message="Couldn't load repositories." onRetry={repos.reload} />
  return (
    <section aria-label="Project repositories" className="project-related">
      <h2 className="section-title">Repositories</h2>
      <p className="muted small">Where this project's code lives; link every repository it spans. Agents see them with the project and clone with their own Git access. With GitHub sync on (Agents page), the latest commit, open pull requests, and release show here and for agents.</p>
      {repos.data.length === 0 ? <p className="muted">No repositories linked yet.</p> : (
        <ul className="repo-list">
          {repos.data.map((repo) => (
            <li key={repo.id}>
              <div>
                <strong>{repo.web_url ? <a href={repo.web_url} target="_blank" rel="noreferrer noopener">{repo.repo}</a> : repo.repo}{repo.role && <span className="muted"> · {repo.role}</span>}</strong>
                <span className="muted small"><code>{repo.url}</code>{repo.branch && ` · branch ${repo.branch}`}{repo.path && ` · folder ${repo.path}`}</span>
                {repoActivity(repo)}
                {repo.note && <span className="small">{repo.note}</span>}
                <span className="muted small">Linked by {repo.added_by === 'ledger-admin' ? 'you' : repo.added_by} · <Timestamp iso={repo.created_at} /></span>
              </div>
              <button type="button" className="btn btn-danger-quiet" disabled={busy} onClick={() => setTarget(repo)}>Unlink</button>
            </li>
          ))}
        </ul>
      )}
      <form className="form repo-form" aria-label="Link a repository" onSubmit={(event) => void link(event)}>
        <div className="form-grid">
          <label className="span-2">Repository URL<input required maxLength={500} value={input.url} onChange={(event) => set('url', event.target.value)} placeholder="https://github.com/owner/repo or git@github.com:owner/repo.git" /></label>
          <label>Role<input maxLength={60} value={input.role} onChange={(event) => set('role', event.target.value)} placeholder="backend, android, docs…" /></label>
          <label>Branch<input maxLength={200} value={input.branch} onChange={(event) => set('branch', event.target.value)} placeholder="Default branch" /></label>
          <label>Folder<input maxLength={300} value={input.path} onChange={(event) => set('path', event.target.value)} placeholder="For a monorepo" /></label>
          <label>Note<input maxLength={500} value={input.note} onChange={(event) => set('note', event.target.value)} /></label>
        </div>
        {error && <p className="field-error" role="alert">{error}</p>}
        <div className="form-actions"><span className="muted small">Never paste a token into the URL.</span><button type="submit" className="btn btn-primary" disabled={busy || !input.url.trim()}>Link repository</button></div>
      </form>
      <ConfirmDialog open={target !== null} title={`Unlink ${target?.repo ?? ''}?`} confirmLabel="Unlink" busy={busy} onCancel={() => setTarget(null)} onConfirm={() => void unlink()}>
        <p>Agents will no longer see this repository with the project. The repository itself is not touched.</p>
      </ConfirmDialog>
    </section>
  )
}

/** The project's "⋯" menu. Delete lives here, out of the main row; it asks for the slug and moves the project to Trash. */
function DeleteProject({ project }: { project: Project }) {
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState<DeletionPreview | null>(null)
  const toast = useToast()
  const undo = useUndo()
  const start = async () => {
    setOpen(true)
    setTyped('')
    setPreview(null)
    try {
      setPreview(await api.deletionPreview(project.slug))
    } catch (failure) {
      toast(describeError(failure), 'error')
    }
  }
  const confirm = async () => {
    setBusy(true)
    try {
      const result = await api.deleteProject(project.slug)
      setOpen(false)
      undo(`${project.name} moved to Trash.`, result.action_id)
      navigate('/projects')
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <OverflowMenu label={`More actions for ${project.name}`} items={[{ label: 'Delete project', onSelect: () => void start(), danger: true }]} />
      <ConfirmDialog open={open} title={`Delete ${project.name}?`} confirmLabel="Delete project" busy={busy} confirmDisabled={typed !== project.slug}
        onConfirm={() => void confirm()} onCancel={() => setOpen(false)}>
        <p>
          {preview
            ? `This moves the project and its ${preview.entries} ${preview.entries === 1 ? 'entry' : 'entries'} to Trash for 30 days. ${preview.handoffs} ${preview.handoffs === 1 ? 'handoff' : 'handoffs'} (${preview.files} ${preview.files === 1 ? 'file' : 'files'}) stay but are unlinked until you restore it.`
            : 'Counting what this project contains…'}
        </p>
        <label>
          Type <code>{project.slug}</code> to confirm
          <input value={typed} onChange={(event) => setTyped(event.target.value)} autoComplete="off" spellCheck={false} />
        </label>
      </ConfirmDialog>
    </>
  )
}

const PROJECT_TABS: { id: ProjectView; label: string; path: string }[] = [
  { id: 'activity', label: 'Activity', path: '' },
  { id: 'todos', label: 'Todos', path: '/todos' },
  { id: 'decisions', label: 'Decisions', path: '/decisions' },
  { id: 'details', label: 'Details', path: '/details' },
  { id: 'handoffs', label: 'Handoffs', path: '/handoffs' },
  { id: 'files', label: 'Files', path: '/files' },
  { id: 'repos', label: 'Repos', path: '/repos' },
]

/** Where the project stands: what needs you, how much is open, who worked on it this week, and its week in a few lines. */
function ProjectStatus({ slug, summary }: { slug: string; summary: ProjectSummary | undefined }) {
  const [expanded, setExpanded] = useState(false)
  // Offer "read more" only when the three lines actually cut the text, at any width.
  const [clipped, setClipped] = useState(false)
  const digest = useRef<HTMLParagraphElement>(null)
  useLayoutEffect(() => {
    const element = digest.current
    if (!element) return
    const measure = () => setClipped(element.scrollHeight > element.clientHeight + 1)
    measure()
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : null
    observer?.observe(element)
    return () => observer?.disconnect()
  }, [summary?.digest, summary?.status_title, summary?.status_body])
  if (!summary) return null
  const base = `/projects/${encodeURIComponent(slug)}`
  const blocked = summary.status_state === 'blocked'
  const latest = summary.digest || summary.status_title || summary.status_body
  return (
    <section className="project-status" aria-label="Project status">
      <ul className="stat-tiles">
        <li data-tone={summary.needs_you > 0 ? 'ask' : undefined}>
          <Link to="/"><strong>{summary.needs_you}</strong><span>{summary.needs_you === 1 ? 'question waits for you' : 'questions wait for you'}</span></Link>
        </li>
        <li><Link to={`${base}/todos`}><strong>{summary.open_todos}</strong><span>open {summary.open_todos === 1 ? 'todo' : 'todos'}</span></Link></li>
        <li><Link to={base}><strong>{summary.week_entries}</strong><span>{summary.week_entries === 1 ? 'entry' : 'entries'} this week</span></Link></li>
        <li>
          <div><strong>{summary.week_agents.length}</strong><span>{summary.week_agents.length === 0 ? (summary.last_entry_at ? <>quiet; last entry <Timestamp iso={summary.last_entry_at} /></> : 'agents active') : summary.week_agents.map(writerName).join(', ')}</span></div>
        </li>
      </ul>
      {blocked && <p className="project-status-blocked"><HealthBadge state={summary.status_state} /> {summary.status_detail || summary.status_title}</p>}
      {latest ? (
        <div className="project-week">
          <p className="eyebrow">{summary.digest ? 'This week' : 'Latest status'}</p>
          {/* Until the AI summarises the week, the latest status stands in. */}
          <p ref={digest} className={expanded ? 'digest' : 'digest clamp'}>{latest}</p>
          {(clipped || expanded) && <button type="button" className="link-button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>{expanded ? 'Show less' : 'Read the whole week'}</button>}
        </div>
      ) : <p className="muted">No status yet. A weekly summary appears once agents have written here.</p>}
    </section>
  )
}

// ResearchShare is the owner's switch for letting research sandboxes, which browse the open web, see this
// project's summary in their task context.
function ResearchShare({ project, onChange }: { project: Project; onChange: (visible: boolean) => void }) {
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const change = async (visible: boolean) => {
    setBusy(true)
    try {
      onChange((await api.setProjectResearch(project.slug, visible)).research_visible)
      toast(visible ? 'Research runs now see this project.' : 'Research runs no longer see this project.')
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }
  return (
    <label className="check research-share">
      <input type="checkbox" checked={project.research_visible} disabled={busy} onChange={(event) => void change(event.target.checked)} />
      <span><strong>Share with research runs</strong><span className="muted small">Research sandboxes browse the open web. When this is on, their task context includes this project's type, goal, description, and stack.</span></span>
    </label>
  )
}

function ProjectDetail({ slug, view, summary, onRetrySummary, onSaved }: { slug: string; view: ProjectView; summary: ProjectSummary | undefined; onRetrySummary?: (() => void) | undefined; onSaved: (project: Project) => void }) {
  const detail = useResource(() => api.getProject(slug), `project:${slug}`, 'project')
  const [editing, setEditing] = useState(false)
  const toast = useToast()

  if (detail.loading) return <Loading label="Loading project…" />
  if (!detail.data) {
    const missing = detail.error === 'project not found'
    return <ErrorState message={missing ? 'Project not found.' : "Couldn't load this project."} onRetry={missing ? undefined : detail.reload} />
  }
  const project = detail.data
  const base = `/projects/${encodeURIComponent(slug)}`

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
            {project.deadline && <span>Due {project.deadline}</span>}
          </div>
          {project.goal && <p className="project-goal">{project.goal}</p>}
        </div>
        {!editing && (
          <div className="detail-actions">
            <button type="button" className="btn" onClick={() => setEditing(true)}>
              Edit project
            </button>
            <DeleteProject project={project} />
          </div>
        )}
      </header>
      {detail.stale && <StaleNotice message="Showing the last loaded version; refresh failed." onRetry={detail.reload} />}
      {editing ? (
        <ProjectForm
          mode="edit"
          project={project}
          onCancel={() => setEditing(false)}
          onSaved={(saved) => {
            detail.update(() => saved)
            setEditing(false)
            onSaved(saved)
            toast('Project saved.')
          }}
        />
      ) : (
        <>
          {/* The summary comes from a separate request that can fail or go stale on its own. */}
          {onRetrySummary && <StaleNotice message={summary ? "This project's week, health, and open questions may be out of date." : "Couldn't load this project's week, health, and open questions."} onRetry={onRetrySummary} />}
          <ProjectStatus slug={slug} summary={summary} />
          <nav className="detail-tabs" aria-label="Project sections">
            {PROJECT_TABS.map((tab) => (
              <Link key={tab.id} to={base + tab.path} aria-current={view === tab.id ? 'page' : undefined}>
                {tab.label}{tab.id === 'todos' && summary && summary.open_todos > 0 && <span className="count">{summary.open_todos}</span>}
              </Link>
            ))}
          </nav>
          {view === 'handoffs' ? <ProjectHandoffs slug={slug} />
            : view === 'files' ? <ProjectFiles slug={slug} />
            : view === 'repos' ? <ProjectRepos slug={slug} />
            : view === 'details' ? (
              <>
              <ResearchShare project={project} onChange={(visible) => detail.update((current) => ({ ...current, research_visible: visible }))} />
              <ul className="meta-grid" aria-label="Project details">
                {META_FIELDS.map((field) => (
                  <li key={field.key}>
                    <span className="meta-label">{field.label}</span>
                    <span className="meta-value">{String(project[field.key] ?? '') || <span className="muted">—</span>}</span>
                  </li>
                ))}
              </ul>
              </>
            ) : <EntrySplit><EntriesView key={view} view={view} fixedProject={slug} /></EntrySplit>}
        </>
      )}
    </article>
  )
}

export function ProjectsPage({ slug, view = 'activity' }: { slug?: string | undefined; view?: ProjectView }) {
  const list = useResource(() => api.listProjects(), 'projects', 'project entry')
  const summaries = useResource(api.getProjectSummaries, 'project-summaries', LIVE)
  const [filter, setFilter] = useState('')
  const [tier, setTier] = useState('all')
  const toast = useToast()
  const summaryOf = (projectSlug: string) => summaries.data?.projects.find((summary) => summary.slug === projectSlug)

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return (list.data ?? []).filter((project) => (tier === 'all' || project.tier === tier) && (needle === '' || project.name.toLowerCase().includes(needle) || project.slug.includes(needle)))
  }, [list.data, filter, tier])
  const tierCount = (option: string) => (list.data ?? []).filter((project) => option === 'all' || project.tier === option).length

  const upsertInList = (saved: Project) => {
    list.update((projects) => {
      const existing = projects.find((project) => project.slug === saved.slug)
      const merged = existing && saved.last_entry_at === undefined && existing.last_entry_at !== undefined ? { ...saved, last_entry_at: existing.last_entry_at } : saved
      return [merged, ...projects.filter((project) => project.slug !== saved.slug)].sort((a, b) => a.slug.localeCompare(b.slug))
    })
    // The summaries carry the name, tier, and deadline too.
    summaries.reload()
  }
  const mode = slug ? 'detail' : 'list'

  return (
    <div className="split" data-mode={mode}>
      <section className="pane pane-list" aria-label="Project list">
        <header className="page-head">
          <h1>Projects</h1>
          <Link to="/projects/_new" className="btn btn-primary">
            <Icon name="plus" /> New project
          </Link>
          <p className="muted small">Open a project for its week, activity, todos, and decisions.</p>
        </header>
        <div className="filters">
          <label className="visually-hidden" htmlFor="project-filter">
            Filter projects
          </label>
          <input id="project-filter" type="search" placeholder="Name or slug" value={filter} onChange={(event) => setFilter(event.target.value)} autoComplete="off" />
          <fieldset className="segmented">
            <legend className="visually-hidden">Tier</legend>
            <div>
            {['all', ...TIERS].map((option) => (
              <label key={option}>
                <input type="radio" name="tier" value={option} checked={tier === option} onChange={() => setTier(option)} />
                <span>
                  {option === 'all' ? 'All' : option}
                  {list.data && <span className="count" aria-hidden="true">{tierCount(option)}</span>}
                </span>
              </label>
            ))}
            </div>
          </fieldset>
        </div>
        {list.loading && <Loading label="Loading projects…" />}
        {!list.loading && !list.data && <ErrorState message="Couldn't load projects." onRetry={list.reload} />}
        {list.stale && <StaleNotice message="Project list may be out of date." onRetry={list.reload} />}
        {list.data && visible.length === 0 && <p className="muted">No projects match.</p>}
        {list.data && visible.length > 0 && (
          <ul className="project-list" aria-label="Projects">
            {visible.map((project) => {
              const summary = summaryOf(project.slug)
              return (
                <li key={project.slug}>
                  <Link to={`/projects/${project.slug}`} aria-current={project.slug === slug ? 'page' : undefined}>
                    <span className="project-name">{project.name}</span>
                    <span className="muted small">{project.last_entry_at ? <Timestamp iso={project.last_entry_at} /> : 'no entries'}</span>
                    <span className="project-tags">
                      <TierBadge tier={project.tier} />
                      <HealthBadge state={summary?.status_state ?? ''} />
                      {summary && summary.needs_you > 0 && <span className="badge" data-focus="ask">{summary.needs_you} for you</span>}
                      {summary && summary.open_todos > 0 && <span className="muted small">{summary.open_todos} open</span>}
                    </span>
                  </Link>
                </li>
              )
            })}
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
          <ProjectDetail key={slug} slug={slug} view={view} summary={summaryOf(slug)} onRetrySummary={summaries.stale || (!summaries.loading && !summaries.data) ? summaries.reload : undefined} onSaved={upsertInList} />
        ) : summaries.loading ? (
          <Loading label="Loading this week…" />
        ) : !summaries.data ? (
          <ErrorState message="Couldn't load this week's project summary." onRetry={summaries.reload} />
        ) : summaries.data.projects.length > 0 ? (
          <section aria-labelledby="all-projects-title">
            <h2 id="all-projects-title" className="section-title">All projects this week</h2>
            {summaries.stale && <StaleNotice message="This summary may be out of date." onRetry={summaries.reload} />}
            <ProjectSummaryTable projects={summaries.data.projects} />
          </section>
        ) : (
          <EmptyState>
            <p>Select a project to see what its agents did, its todos, and its decisions.</p>
          </EmptyState>
        )}
      </section>
    </div>
  )
}
