import { api, TIERS, type Project } from '../api'
import { useResource } from '../hooks/useResource'
import { Link } from '../router'
import { EmptyState, ErrorState, KindBadge, Loading, StaleNotice, Timestamp } from '../components/ui'

const TIER_LABEL: Record<string, string> = { focus: 'Focus', maintain: 'Maintain', park: 'Park' }

function TierTable({ tier, projects }: { tier: string; projects: Project[] }) {
  return (
    <section aria-labelledby={`tier-${tier}`} className="panel">
      <h2 id={`tier-${tier}`} className="panel-title">
        {TIER_LABEL[tier]} <span className="count">{projects.length}</span>
      </h2>
      <table className="table">
        <thead>
          <tr>
            <th scope="col">Project</th>
            <th scope="col">Slug</th>
            <th scope="col" className="num">
              h/wk
            </th>
            <th scope="col">Deadline</th>
            <th scope="col">Last entry</th>
          </tr>
        </thead>
        <tbody>
          {projects.map((project) => (
            <tr key={project.slug}>
              <td data-label="Project">
                <Link to={`/projects/${project.slug}`}>{project.name}</Link>
              </td>
              <td data-label="Slug">
                <code>{project.slug}</code>
              </td>
              <td data-label="Hours per week" className="num">{project.hours_wk}</td>
              <td data-label="Deadline">{project.deadline || <span className="muted">—</span>}</td>
              <td data-label="Last entry">{project.last_entry_at ? <Timestamp iso={project.last_entry_at} /> : <span className="muted">none</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}

export function OverviewPage() {
  const overview = useResource(api.getOverview, 'overview')
  if (overview.loading) return <Loading label="Loading overview…" />
  if (!overview.data) return <ErrorState message="Couldn't load the overview." onRetry={overview.reload} />
  const { counts, projects, recent_entries: recent } = overview.data
  const grouped = TIERS.map((tier) => ({ tier, projects: projects.filter((project) => project.tier === tier) })).filter((group) => group.projects.length > 0)

  return (
    <>
      <header className="page-head">
        <h1>Overview</h1>
        <ul className="counts" aria-label="Counts">
          <li>
            <span>Projects</span>
            <strong>{counts.projects}</strong>
          </li>
          <li>
            <span>Entries</span>
            <strong>{counts.entries}</strong>
          </li>
          <li>
            <span>OAuth clients</span>
            <strong>{counts.oauth_clients}</strong>
          </li>
          <li>
            <span>Active tokens</span>
            <strong>{counts.active_access_tokens}</strong>
          </li>
          <li>
            <span>Admin sessions</span>
            <strong>{counts.active_admin_sessions}</strong>
          </li>
        </ul>
      </header>
      {overview.stale && <StaleNotice message="Showing the last loaded overview; refresh failed." onRetry={overview.reload} />}
      <div className="overview-grid">
        <div className="overview-projects">
          {grouped.length === 0 ? (
            <EmptyState>
              <p>No projects yet.</p>
              <Link className="btn btn-primary" to="/projects/_new">
                Create a project
              </Link>
            </EmptyState>
          ) : (
            grouped.map((group) => <TierTable key={group.tier} tier={group.tier} projects={group.projects} />)
          )}
        </div>
        <section aria-labelledby="recent-title" className="panel">
          <h2 id="recent-title" className="panel-title">
            Recent activity
          </h2>
          {recent.length === 0 ? (
            <p className="muted">No memory activity yet.</p>
          ) : (
            <ol className="activity">
              {recent.map((entry) => (
                <li key={entry.id}>
                  <div className="entry-head">
                    <KindBadge kind={entry.kind} />
                    <Link to={`/projects/${entry.slug}`}>{entry.project_name}</Link>
                    <Timestamp iso={entry.created_at} />
                    <code className="muted">{entry.source}</code>
                  </div>
                  <p className="entry-body clamp">{entry.body}</p>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>
    </>
  )
}
