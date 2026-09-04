import { useState, type FormEvent } from 'react'
import { api, ENTRY_KINDS, type SearchRequest } from '../api'
import { ErrorState, KindBadge, Loading, Timestamp } from '../components/ui'
import { useResource } from '../hooks/useResource'
import { Link, navigate, useLocation } from '../router'

const LIMIT = 20
const DEGRADED: Record<string, string> = {
  vector: 'Vector retrieval unavailable; showing lexical matches.',
  rerank: 'Reranking unavailable; results use fused ranking only.',
}

function Results({ request }: { request: SearchRequest }) {
  const key = JSON.stringify(request)
  const results = useResource(() => api.search(request), key)
  if (results.loading) return <Loading label="Searching…" />
  if (!results.data) return <ErrorState message="Search is unavailable right now." onRetry={results.reload} />
  const { hits, degraded } = results.data
  return (
    <>
      {degraded.length > 0 && (
        <p className="notice" role="status" aria-label="Degraded retrieval">
          {degraded.map((mode) => DEGRADED[mode] ?? `${mode} unavailable.`).join(' ')}
        </p>
      )}
      {hits.length === 0 ? (
        <p className="muted">No results for “{request.q}”.</p>
      ) : (
        <ol className="results" aria-label="Results">
          {hits.map((hit, index) => (
            <li key={hit.ref}>
              <div className="entry-head">
                <span className="rank">{index + 1}</span>
                <KindBadge kind={hit.kind} />
                <Link to={`/projects/${hit.project_slug}`}>{hit.project_name || hit.project_slug}</Link>
                <code>{hit.ref}</code>
                {hit.created_at && <Timestamp iso={hit.created_at} />}
                {hit.source && <code className="muted">via {hit.source}</code>}
                <span className="muted small score">score {hit.score.toFixed(2)}</span>
              </div>
              <p className="entry-body">{hit.snippet}</p>
            </li>
          ))}
        </ol>
      )}
    </>
  )
}

export function SearchPage() {
  const { query } = useLocation()
  const q = query.get('q') ?? ''
  const project = query.get('project') ?? ''
  const kind = query.get('kind') ?? ''
  const [draft, setDraft] = useState({ q, project, kind })
  const [hint, setHint] = useState(false)
  const [nonce, setNonce] = useState(0)
  const projects = useResource(() => api.listProjects(), 'projects')

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = draft.q.trim()
    if (trimmed === '') {
      setHint(true)
      return
    }
    setHint(false)
    setNonce((value) => value + 1)
    const params = new URLSearchParams({ q: trimmed })
    if (draft.project) params.set('project', draft.project)
    if (draft.kind) params.set('kind', draft.kind)
    navigate(`/search?${params.toString()}`)
  }

  const request: SearchRequest | null = q ? { q, limit: LIMIT, ...(project ? { project } : {}), ...(kind ? { kind } : {}) } : null

  return (
    <>
      <header className="page-head">
        <h1>Search</h1>
      </header>
      <form className="search-form" role="search" onSubmit={submit}>
        <label htmlFor="search-q">Search memory</label>
        <div className="search-row">
          <input id="search-q" type="search" placeholder="Query" value={draft.q} onChange={(event) => setDraft({ ...draft, q: event.target.value })} autoFocus autoComplete="off" />
          <button type="submit" className="btn btn-primary">
            Search
          </button>
        </div>
        <div className="search-filters">
          <label>
            Project
            <select value={draft.project} onChange={(event) => setDraft({ ...draft, project: event.target.value })}>
              <option value="">All projects</option>
              {(projects.data ?? []).map((item) => (
                <option key={item.slug} value={item.slug}>
                  {item.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Kind
            <select value={draft.kind} onChange={(event) => setDraft({ ...draft, kind: event.target.value })}>
              <option value="">Any kind</option>
              <option value="project">project</option>
              {ENTRY_KINDS.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </label>
        </div>
        {(hint || !request) && <p className="muted small">Type a query to search project memory. Lexical and semantic ranking are fused; filters narrow the top results.</p>}
      </form>
      {request && <Results key={`${JSON.stringify(request)}#${nonce}`} request={request} />}
    </>
  )
}
