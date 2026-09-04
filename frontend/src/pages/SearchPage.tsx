import { useState, type FormEvent, type ReactNode } from 'react'
import { api, ENTRY_KINDS, type Project, type SearchHit, type SearchRequest } from '../api'
import { ErrorState, Icon, Loading, Timestamp } from '../components/ui'
import { useResource } from '../hooks/useResource'
import { Link, navigate, useLocation } from '../router'

const LIMIT = 20
const DEGRADED: Record<string, string> = {
  vector: 'Vector retrieval unavailable; showing lexical matches.',
  rerank: 'Reranking unavailable; results use fused ranking only.',
}

type Scope = 'everything' | 'projects' | 'entries'

function scopeFor(kind: string): Scope {
  if (kind === 'project') return 'projects'
  if (kind !== '') return 'entries'
  return 'everything'
}

function highlighted(text: string, query: string): ReactNode {
  const words = [...new Set(query.trim().split(/\s+/).filter((word) => word.length > 1))]
  if (words.length === 0) return text
  const wanted = new Set(words.map((word) => word.toLocaleLowerCase()))
  const pattern = new RegExp(`(${words.map((word) => word.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'gi')
  return text.split(pattern).map((part, index) =>
    wanted.has(part.toLocaleLowerCase()) ? <mark key={`${part}-${index}`}>{part}</mark> : part,
  )
}

function ResultItem({ hit, query }: { hit: SearchHit; query: string }) {
  const [headline = hit.snippet, ...detail] = hit.snippet.split('\n')
  const title = hit.kind === 'project' ? hit.project_name || hit.project_slug : headline
  const excerpt = hit.kind === 'project' ? hit.snippet : detail.join(' ')
  return (
    <li className="search-result" data-kind={hit.kind}>
      <p className="result-kind">{hit.kind}</p>
      <Link className="result-title" to={`/projects/${hit.project_slug}`}>
        {highlighted(title, query)}
      </Link>
      {excerpt !== '' && excerpt !== title && <p className="result-excerpt">{highlighted(excerpt, query)}</p>}
      <div className="result-meta">
        <span>{hit.project_name || hit.project_slug}</span>
        {hit.created_at && <Timestamp iso={hit.created_at} />}
        <span className="result-score">{Math.round(hit.score * 100)}%</span>
      </div>
    </li>
  )
}

function Results({ request }: { request: SearchRequest }) {
  const key = JSON.stringify(request)
  const results = useResource(() => api.search(request), key, 'project entry chunk')
  if (results.loading) return <Loading label="Searching…" />
  if (!results.data) return <ErrorState message="Search is unavailable right now." onRetry={results.reload} />
  const { hits, degraded } = results.data
  const projects = new Set(hits.map((hit) => hit.project_slug).filter(Boolean)).size
  const best = hits.slice(0, 3)
  const more = hits.slice(3)
  return (
    <section className="search-results" aria-label="Results">
      {degraded.length > 0 && (
        <p className="notice" role="status" aria-label="Degraded retrieval">
          {degraded.map((mode) => DEGRADED[mode] ?? `${mode} unavailable.`).join(' ')}
        </p>
      )}
      {hits.length === 0 ? (
        <p className="muted">No results for “{request.q}”.</p>
      ) : (
        <>
          <p className="search-summary">
            {hits.length} {hits.length === 1 ? 'match' : 'matches'} across {projects} {projects === 1 ? 'project' : 'projects'}
          </p>
          <section className="result-group" aria-labelledby="best-matches">
            <h2 id="best-matches">Best matches</h2>
            <ol className="results">
              {best.map((hit) => <ResultItem key={hit.ref} hit={hit} query={request.q} />)}
            </ol>
          </section>
          {more.length > 0 && (
            <section className="result-group" aria-labelledby="more-results">
              <h2 id="more-results">More results</h2>
              <ol className="results">
                {more.map((hit) => <ResultItem key={hit.ref} hit={hit} query={request.q} />)}
              </ol>
            </section>
          )}
        </>
      )}
    </section>
  )
}

function SearchContent({ q, project, kind, projects }: { q: string; project: string; kind: string; projects: Project[] }) {
  const [draft, setDraft] = useState({ q, project, kind })
  const [filtersOpen, setFiltersOpen] = useState(project !== '' || !['', 'project', 'entry'].includes(kind))
  const [hint, setHint] = useState(false)
  const [nonce, setNonce] = useState(0)

  const run = (next: typeof draft) => {
    const trimmed = next.q.trim()
    if (trimmed === '') {
      setHint(true)
      return
    }
    setHint(false)
    const params = new URLSearchParams({ q: trimmed })
    if (next.project) params.set('project', next.project)
    if (next.kind) params.set('kind', next.kind)
    navigate(`/search?${params.toString()}`)
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setNonce((value) => value + 1)
    run(draft)
  }

  const selectScope = (scope: Scope) => {
    const next = { ...draft, kind: scope === 'everything' ? '' : scope === 'projects' ? 'project' : 'entry' }
    setDraft(next)
    if (next.q.trim()) run(next)
  }

  const request: SearchRequest | null = q ? { q, limit: LIMIT, ...(project ? { project } : {}), ...(kind ? { kind } : {}) } : null
  const scope = scopeFor(draft.kind)

  return (
    <div className="search-page">
      <form className="search-workspace" role="search" onSubmit={submit}>
        <div className="search-command">
          <h1 className="search-brand" aria-label="Search">Ledger / Search</h1>
          <div className="search-query-row">
            <label className="visually-hidden" htmlFor="search-q">Search memory</label>
            <input id="search-q" type="search" placeholder="What do you need to recall?" value={draft.q} onChange={(event) => setDraft({ ...draft, q: event.target.value })} autoFocus autoComplete="off" />
            <button type="submit" className="search-submit" aria-label="Search">
              <Icon name="arrow" size={28} />
            </button>
          </div>
        </div>
        <div className="search-scopes" role="group" aria-label="Search scope">
          {(['everything', 'projects', 'entries'] as const).map((item) => (
            <button key={item} type="button" className="search-scope" aria-pressed={scope === item} onClick={() => selectScope(item)}>
              {item[0]!.toUpperCase() + item.slice(1)}
            </button>
          ))}
          <button type="button" className="search-filter-toggle" aria-label="Filters" aria-expanded={filtersOpen} onClick={() => setFiltersOpen((open) => !open)}>
            <Icon name="filter" size={23} />
          </button>
        </div>
        {filtersOpen && (
          <div className="search-filters">
            <label>
              Project
              <select value={draft.project} onChange={(event) => setDraft({ ...draft, project: event.target.value })}>
                <option value="">All projects</option>
                {projects.map((item) => (
                  <option key={item.slug} value={item.slug}>{item.name}</option>
                ))}
              </select>
            </label>
            <label>
              Kind
              <select value={scope === 'projects' ? 'project' : draft.kind} onChange={(event) => setDraft({ ...draft, kind: event.target.value })}>
                <option value="">Any kind</option>
                <option value="entry">All entry types</option>
                <option value="project">Project</option>
                {ENTRY_KINDS.map((option) => <option key={option} value={option}>{option}</option>)}
              </select>
            </label>
          </div>
        )}
      </form>
      <div className="search-body">
        {(hint || !request) && <p className="search-intro">Type a query to search decisions, notes, tasks, and projects. Use filters only when you need to narrow the result set.</p>}
        {request && <Results key={`${JSON.stringify(request)}#${nonce}`} request={request} />}
      </div>
    </div>
  )
}

export function SearchPage() {
  const { query } = useLocation()
  const q = query.get('q') ?? ''
  const project = query.get('project') ?? ''
  const kind = query.get('kind') ?? ''
  const projects = useResource(() => api.listProjects(), 'projects', 'project')
  const locationKey = JSON.stringify([q, project, kind])
  return <SearchContent key={locationKey} q={q} project={project} kind={kind} projects={projects.data ?? []} />
}
