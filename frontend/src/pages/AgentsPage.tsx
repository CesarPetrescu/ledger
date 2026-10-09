import { Link } from '../router'
import { api, type AgentSummary } from '../api'
import { titleOf } from '../components/entries'
import { EmptyState, ErrorState, Loading, StaleNotice, Timestamp } from '../components/ui'
import { useResource } from '../hooks/useResource'

/** Agents: what each one did lately and what it waits on you for. Connecting one and its access live on Access. */
export function AgentsPage() {
  const agents = useResource(api.listAgents, 'agents', 'project entry entry_meta entry_owner_state handoff_message')
  return (
    <>
      <header className="page-head">
        <div>
          <p className="eyebrow">Who writes to Ledger</p>
          <h1>Agents</h1>
        </div>
        <Link to="/access">Manage access <span aria-hidden="true">→</span></Link>
        <p className="muted">What each agent did lately and what it is waiting on you for.</p>
      </header>
      {agents.loading && <Loading label="Loading agents…" />}
      {!agents.loading && !agents.data && <ErrorState message="Couldn't load agents." onRetry={agents.reload} />}
      {agents.stale && <StaleNotice message="Agent activity may be out of date." onRetry={agents.reload} />}
      {agents.data && agents.data.length === 0 && (
        <EmptyState>
          <p>No agent has written to Ledger yet. <Link to="/access">Connect one on Access</Link>; its notes, decisions, and todos will show up here and in your Inbox.</p>
        </EmptyState>
      )}
      {agents.data && agents.data.length > 0 && (
        <ul className="agent-cards" aria-label="Agents">
          {agents.data.map((agent) => <AgentCard key={agent.name} agent={agent} />)}
        </ul>
      )}
    </>
  )
}

function AgentCard({ agent }: { agent: AgentSummary }) {
  const activity = `/table?view=activity&source=${encodeURIComponent(agent.name)}`
  return (
    <li className="agent-card">
      <div className="agent-card-head">
        <h2><code>{agent.name}</code></h2>
        <span className="muted small">{agent.last_active ? <>Active <Timestamp iso={agent.last_active} /></> : 'No entries yet'}</span>
      </div>
      <p className="agent-card-facts">
        {agent.week_entries > 0 ? `${agent.week_entries} ${agent.week_entries === 1 ? 'entry' : 'entries'} this week` : 'Quiet this week'} · {agent.entries} in total
        {agent.projects.length > 0 && <> · {agent.projects.map((project, index) => <span key={project.slug}>{index > 0 && ', '}<Link to={`/projects/${encodeURIComponent(project.slug)}`}>{project.name}</Link></span>)}</>}
      </p>
      {(agent.open_asks > 0 || agent.handoffs > 0) && (
        <p className="agent-card-needs">
          {agent.open_asks > 0 && <Link to="/">{agent.open_asks} {agent.open_asks === 1 ? 'question waits' : 'questions wait'} for your answer</Link>}
          {agent.open_asks > 0 && agent.handoffs > 0 && ' · '}
          {agent.handoffs > 0 && <Link to="/handoffs">Working on {agent.handoffs} {agent.handoffs === 1 ? 'handoff' : 'handoffs'}</Link>}
        </p>
      )}
      {agent.latest.length > 0 && (
        <ul className="agent-card-latest" aria-label={`Latest from ${agent.name}`}>
          {agent.latest.map((entry) => (
            <li key={entry.id}><Link to={`/entries/${entry.id}`}>{titleOf(entry)}</Link> <span className="muted small">· {entry.project_name} · <Timestamp iso={entry.created_at} /></span></li>
          ))}
        </ul>
      )}
      <Link className="small" to={activity}>All of its activity</Link>
    </li>
  )
}
