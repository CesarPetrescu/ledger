import { Link } from '../router'
import { useState, type FormEvent } from 'react'
import { api, describeError, type AgentSummary, type ApiKey, type Client } from '../api'
import { titleOf } from '../components/entries'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Loading, StaleNotice, Timestamp } from '../components/ui'
import { useResource } from '../hooks/useResource'

const KIND_LABEL: Record<Client['kind'], string> = { dcr: 'Dynamic registration', cimd: 'Client ID metadata', device: 'Connected machine' }
const PAGE_SIZE = 50

/** Agents: what each one did lately, how to connect one, and the apps with access. */
export function AgentsPage() {
  const agents = useResource(api.listAgents, 'agents', 'project entry entry_meta entry_owner_state handoff_message')
  return (
    <>
      <header className="page-head">
        <div>
          <p className="eyebrow">Who writes to Ledger</p>
          <h1>Agents</h1>
        </div>
        <Link to="/connect" className="btn">Connect a machine</Link>
        <p className="muted">What each agent did lately, what it is waiting on you for, and the apps that can read or write your projects.</p>
      </header>
      {agents.loading && <Loading label="Loading agents…" />}
      {!agents.loading && !agents.data && <ErrorState message="Couldn't load agents." onRetry={agents.reload} />}
      {agents.stale && <StaleNotice message="Agent activity may be out of date." onRetry={agents.reload} />}
      {agents.data && agents.data.length === 0 && (
        <EmptyState>
          <p>No agent has written to Ledger yet. Connect one below; its notes, decisions, and todos will show up here and in your Inbox.</p>
        </EmptyState>
      )}
      {agents.data && agents.data.length > 0 && (
        <ul className="agent-cards" aria-label="Agents">
          {agents.data.map((agent) => <AgentCard key={agent.name} agent={agent} />)}
        </ul>
      )}
      <ConnectGuide />
      <ConnectedApps />
      <ApiKeys />
      <ApprovalPassword />
    </>
  )
}

/** API keys let a server use /api/v1 without signing in, such as Adastrion Core dispatching research. */
function ApiKeys() {
  const keys = useResource(api.listApiKeys, 'api-keys', 'api_key')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<{ name: string; secret: string } | null>(null)
  const [target, setTarget] = useState<ApiKey | null>(null)
  const toast = useToast()

  async function create(event: FormEvent) {
    event.preventDefault()
    if (busy || !name.trim()) return
    setBusy(true)
    try {
      const result = await api.createApiKey(name.trim())
      setCreated({ name: result.key.name, secret: result.secret })
      setName('')
      keys.reload()
    } catch (failure) { toast(describeError(failure), 'error') }
    finally { setBusy(false) }
  }

  async function copy(secret: string) {
    try {
      await navigator.clipboard.writeText(secret)
      toast('Key copied.')
    } catch { toast('Clipboard access failed; select the key and copy it.', 'error') }
  }

  async function revoke() {
    if (!target) return
    setBusy(true)
    try {
      await api.revokeApiKey(target.id)
      toast(`Revoked ${target.name}.`)
      setTarget(null)
      keys.reload()
    } catch (failure) { toast(describeError(failure), 'error') }
    finally { setBusy(false) }
  }

  return <section className="connected-apps" aria-labelledby="api-keys-title">
    <h2 id="api-keys-title" className="section-title">API keys</h2>
    <p className="muted small">A key lets a server use Ledger's research API without signing in, for example Adastrion Core picking up research tasks and opening a chat for each. A key can only dispatch research, and Ledger shows it once.</p>
    {created && <div className="key-reveal" role="status">
      <p><strong>Copy the key for {created.name} now.</strong> Ledger will not show it again.</p>
      <code className="break">{created.secret}</code>
      <div className="form-actions"><button type="button" className="btn btn-primary" onClick={() => void copy(created.secret)}>Copy key</button><button type="button" className="btn" onClick={() => setCreated(null)}>Done</button></div>
    </div>}
    <form className="inline-form" onSubmit={event => void create(event)}>
      <label htmlFor="api-key-name">Key name</label>
      <input id="api-key-name" value={name} onChange={event => setName(event.target.value)} maxLength={100} placeholder="Adastrion Core" disabled={busy} required />
      <button className="btn" disabled={busy || !name.trim()}>Create key</button>
    </form>
    {keys.loading && <Loading label="Loading API keys…" />}
    {!keys.loading && !keys.data && <ErrorState message="Couldn't load API keys." onRetry={keys.reload} />}
    {keys.data && keys.data.length === 0 && <p className="muted">No API keys yet.</p>}
    {keys.data && keys.data.length > 0 && (
      <table className="table clients">
        <caption className="visually-hidden">API keys</caption>
        <thead><tr><th scope="col">Name</th><th scope="col">Key</th><th scope="col">Can</th><th scope="col">Created</th><th scope="col">Last used</th><th scope="col"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody>
          {keys.data.map((key) => (
            <tr key={key.id} className={key.revoked_at ? 'muted' : undefined}>
              <td data-label="Name">{key.name}</td>
              <td data-label="Key"><code>{key.prefix}…</code></td>
              <td data-label="Can">Dispatch research</td>
              <td data-label="Created"><Timestamp iso={key.created_at} /></td>
              <td data-label="Last used">{key.last_used_at ? <Timestamp iso={key.last_used_at} /> : <span className="muted">never</span>}</td>
              <td data-label="Actions">{key.revoked_at ? <span className="muted small">Revoked <Timestamp iso={key.revoked_at} /></span> : <button type="button" className="btn btn-danger-quiet" onClick={() => setTarget(key)}>Revoke</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    )}
    <ConfirmDialog open={target !== null} title={`Revoke ${target?.name ?? ''}?`} confirmLabel="Revoke" busy={busy} onCancel={() => setTarget(null)} onConfirm={() => void revoke()}>
      <p>The key stops working immediately. Research runs it started are stopped and queued again, and their chats lose access to Ledger.</p>
    </ConfirmDialog>
  </section>
}

function ApprovalPassword() {
  const [ownerPassword, setOwnerPassword] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  async function save(event: FormEvent) {
    event.preventDefault()
    if (busy) return
    setSaved(false)
    setError('')
    if (password !== confirmation) { setError('The new passwords do not match.'); return }
    setBusy(true)
    try {
      await api.changeApprovalPassword(ownerPassword, password)
      setOwnerPassword('')
      setPassword('')
      setConfirmation('')
      setSaved(true)
    } catch (failure) { setError(describeError(failure)) }
    finally { setBusy(false) }
  }

  return <section className="connected-apps" aria-labelledby="password-title">
    <h2 id="password-title" className="section-title">Approval password</h2>
    <p className="muted">You can approve apps with your Ledger owner login. To set a new approval password, confirm your owner password below. You do not need the old approval password.</p>
    <form onSubmit={event => void save(event)}>
      <label htmlFor="approval-owner-password">Owner password</label>
      <input id="approval-owner-password" type="password" autoComplete="current-password" value={ownerPassword} onChange={event => setOwnerPassword(event.target.value)} required maxLength={4096} disabled={busy} />
      <label htmlFor="new-approval-password">New approval password</label>
      <input id="new-approval-password" type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.target.value)} required minLength={12} maxLength={4096} disabled={busy} />
      <label htmlFor="confirm-approval-password">Confirm new approval password</label>
      <input id="confirm-approval-password" type="password" autoComplete="new-password" value={confirmation} onChange={event => setConfirmation(event.target.value)} required minLength={12} maxLength={4096} disabled={busy} />
      {error && <p role="alert">{error}</p>}
      {saved && <p role="status">Approval password changed.</p>}
      <div className="form-actions"><button className="btn btn-primary" disabled={busy}>Change approval password</button></div>
    </form>
  </section>
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

/** How to connect each kind of agent, with this server's own address filled in. */
function ConnectGuide() {
  const origin = window.location.origin
  return (
    <section className="connect-guide" aria-labelledby="connect-title">
      <h2 id="connect-title" className="section-title">Connect an agent</h2>
      <dl>
        <div>
          <dt>Claude, ChatGPT, or another MCP app</dt>
          <dd>Add this address as a remote MCP server (in Claude: a custom connector; in ChatGPT: a connector), then approve it in your browser: <code className="break">{origin}/mcp</code></dd>
        </div>
        <div>
          <dt>Claude Code</dt>
          <dd><code className="break">claude mcp add --transport http ledger {origin}/mcp</code></dd>
        </div>
        <div>
          <dt>Codex</dt>
          <dd>Install the Ledger CLI, run <code className="break">ledger connect codex --server {origin}</code>, then approve its code on <Link to="/connect">Connect a machine</Link>.</dd>
        </div>
      </dl>
      <p className="muted small">Agents can read and write every project, not a single one. Revoke an app below to cut its access.</p>
    </section>
  )
}

/** The OAuth apps with access, their tokens, and revocation. */
function ConnectedApps() {
  const [offset, setOffset] = useState(0)
  const page = useResource(() => api.listClients(offset), `clients:${offset}`, 'oauth_client oauth_token')
  const overview = useResource(api.getOverview, 'overview', 'project entry oauth_client oauth_token admin_session')
  const [target, setTarget] = useState<Client | null>(null)
  const [busy, setBusy] = useState(false)
  const toast = useToast()

  const revoke = async () => {
    if (!target) return
    setBusy(true)
    try {
      const result = await api.revokeClient(target.client_id)
      toast(`Revoked ${result.revoked} tokens.`)
      setTarget(null)
      page.reload()
      overview.reload()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="connected-apps" aria-labelledby="apps-title">
      <h2 id="apps-title" className="section-title">Connected apps and access</h2>
      <p className="muted small">Apps that connected through OAuth. A client ID only identifies an app; access tokens allow requests, and refresh tokens let an app renew access without asking you again.</p>
      {overview.data && (
        <ul className="counts" aria-label="Counts">
          <li><span>Projects</span><strong>{overview.data.counts.projects}</strong></li>
          <li><span>Entries</span><strong>{overview.data.counts.entries}</strong></li>
          <li><span>OAuth clients</span><strong>{overview.data.counts.oauth_clients}</strong></li>
          <li><span>Active access tokens</span><strong>{overview.data.counts.active_access_tokens}</strong></li>
          <li><span>Admin sessions</span><strong>{overview.data.counts.active_admin_sessions}</strong></li>
        </ul>
      )}
      {!overview.loading && !overview.data && <StaleNotice message="Couldn't load the counts." onRetry={overview.reload} />}
      {overview.stale && <StaleNotice message="These counts may be out of date." onRetry={overview.reload} />}
      {page.loading && <Loading label="Loading clients…" />}
      {!page.loading && !page.data && <ErrorState message="Couldn't load OAuth clients." onRetry={page.reload} />}
      {page.stale && <StaleNotice message="Client list may be out of date." onRetry={page.reload} />}
      {page.data && page.data.clients.length === 0 && (
        <EmptyState>
          <p>No apps have connected yet. Use one of the ways above.</p>
        </EmptyState>
      )}
      {page.data && page.data.clients.length > 0 && (
        <>
        <table className="table clients">
          <caption className="visually-hidden">OAuth clients</caption>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Type</th>
              <th scope="col">Client ID</th>
              <th scope="col">Redirect URIs</th>
              <th scope="col">Created</th>
              <th scope="col">Last used</th>
              <th scope="col" className="num">
                Access tokens
              </th>
              <th scope="col" className="num">Refresh tokens</th>
              <th scope="col">
                <span className="visually-hidden">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {page.data.clients.map((client) => (
              <tr key={client.client_id}>
                <td data-label="Name">{client.client_name || <span className="muted">unnamed</span>}</td>
                <td data-label="Type">{KIND_LABEL[client.kind]}</td>
                <td data-label="Client ID">
                  <code className="break">{client.client_id}</code>
                </td>
                <td data-label="Redirect URIs">
                  <ul className="plain">
                    {client.redirect_uris.map((uri) => (
                      <li key={uri}>
                        <code className="break">{uri}</code>
                      </li>
                    ))}
                  </ul>
                </td>
                <td data-label="Created">
                  <Timestamp iso={client.created_at} />
                </td>
                <td data-label="Last used">
                  <Timestamp iso={client.last_used_at} />
                </td>
                <td data-label="Access tokens" className="num">{client.active_access_tokens}</td>
                <td data-label="Refresh tokens" className="num">{client.active_refresh_tokens}</td>
                <td data-label="Actions">
                  <button type="button" className="btn btn-danger-quiet" onClick={() => setTarget(client)}>
                    Revoke tokens
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <nav className="form-actions" aria-label="Client pages">
          {offset > 0 && (
            <button type="button" className="btn" onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>
              Previous page
            </button>
          )}
          {page.data.next_offset !== undefined && (
            <button type="button" className="btn" onClick={() => setOffset(page.data!.next_offset!)}>
              Next page
            </button>
          )}
        </nav>
        </>
      )}
      <ConfirmDialog open={target !== null} title={`Revoke tokens for ${target?.client_name || target?.client_id || ''}?`} confirmLabel="Revoke" busy={busy} onCancel={() => setTarget(null)} onConfirm={() => void revoke()}>
        <p>Revokes all access and refresh tokens and invalidates pending browser and device authorizations. The client registration stays, but reconnecting requires a new authorization in your browser.</p>
      </ConfirmDialog>
    </section>
  )
}
