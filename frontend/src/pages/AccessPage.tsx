import { Link } from '../router'
import { useEffect, useRef, useState, type FormEvent, type RefObject } from 'react'
import { api, describeError, setCsrfToken, type ApiKey, type Client, type DeviceSession, type Passkey, type SignInMethod } from '../api'
import { useAuth } from '../auth'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Icon, Loading, StaleNotice, Timestamp } from '../components/ui'
import { useResource, type Resource } from '../hooks/useResource'
import { createPasskey, passkeyCancelled, passkeysSupported, setPasskeyUsedHere } from '../webauthn'

const KIND_LABEL: Record<Client['kind'], string> = { dcr: 'Dynamic registration', cimd: 'Client ID metadata', device: 'Connected machine' }
const PAGE_SIZE = 50

/** Access: how you sign in, how to connect an agent, and everything that can reach Ledger without you signing in. */
export function AccessPage() {
  // Shared with Signed-in devices, which explains the password-only session that added the first passkey.
  const passkeys = useResource(api.listPasskeys, 'passkeys', 'owner_passkey')
  return (
    <>
      <header className="page-head">
        <div>
          <p className="eyebrow">Who can reach Ledger</p>
          <h1>Access</h1>
        </div>
        <Link to="/connect" className="btn">Connect a machine</Link>
        <p className="muted">Manage your passkeys and signed-in devices, connect an agent, see the apps that can read or write your projects and cut their access, and manage API keys, GitHub sync, and the approval password.</p>
      </header>
      <Passkeys list={passkeys} />
      <SignedInDevices passkeysRequired={passkeys.data?.required === true} />
      <ConnectGuide />
      <ConnectedApps />
      <ApiKeys />
      <GitHubSync />
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
              <td data-label="Actions" className="actions">{key.revoked_at ? <span className="muted small">Revoked <Timestamp iso={key.revoked_at} /></span> : <button type="button" className="btn btn-danger-quiet" onClick={() => setTarget(key)}>Revoke</button>}</td>
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

/** The read-only GitHub token the server uses to keep linked repositories' activity fresh. It is
 * write-only here: the console shows its last characters, never the token. */
function GitHubSync() {
  const status = useResource(api.getGitHubSync, 'github-sync', 'github_sync')
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [removing, setRemoving] = useState(false)
  const toast = useToast()

  async function save(event: FormEvent) {
    event.preventDefault()
    if (busy || !token.trim()) return
    setBusy(true)
    setError('')
    try {
      const saved = await api.setGitHubSync(token.trim())
      setToken('')
      status.update(() => saved)
      toast(`GitHub sync connected as ${saved.login ?? 'your account'}.`)
    } catch (failure) { setError(describeError(failure)) }
    finally { setBusy(false) }
  }

  async function remove() {
    setBusy(true)
    try {
      await api.deleteGitHubSync()
      status.update(() => ({ configured: false }))
      setRemoving(false)
      toast('GitHub sync turned off.')
    } catch (failure) { toast(describeError(failure), 'error') }
    finally { setBusy(false) }
  }

  const current = status.data
  return <section className="connected-apps" aria-labelledby="github-sync-title">
    <h2 id="github-sync-title" className="section-title">GitHub sync</h2>
    <p className="muted small">With a read-only token, Ledger checks each linked GitHub repository every 15 minutes: latest commit, open pull requests, latest release. Agents see that with the project. Create a fine-grained token on GitHub with read-only <strong>Metadata</strong>, <strong>Contents</strong>, and <strong>Pull requests</strong> for the repositories you want synced. Ledger stores it encrypted, uses it only on the server, and never shows it again. Agents never receive it.</p>
    {status.loading && <Loading label="Loading GitHub sync…" />}
    {!status.loading && !current && <ErrorState message="Couldn't load GitHub sync." onRetry={status.reload} />}
    {current?.configured && <p>Connected as <strong>{current.login}</strong> with token <code>{current.hint}</code>{current.last_run_at && <> · last checked <Timestamp iso={current.last_run_at} /></>}{current.last_error && <span className="field-error"> · {current.last_error}</span>}</p>}
    <form className="inline-form" onSubmit={event => void save(event)}>
      <label htmlFor="github-token">{current?.configured ? 'Replace token' : 'Token'}</label>
      <input id="github-token" type="password" autoComplete="off" spellCheck={false} value={token} onChange={event => setToken(event.target.value)} maxLength={255} placeholder="github_pat_…" disabled={busy} />
      <button className="btn" disabled={busy || !token.trim()}>{busy ? 'Checking…' : 'Save token'}</button>
      {current?.configured && <button type="button" className="btn btn-danger-quiet" disabled={busy} onClick={() => setRemoving(true)}>Turn off</button>}
    </form>
    {error && <p className="field-error" role="alert">{error}</p>}
    <ConfirmDialog open={removing} title="Turn off GitHub sync?" confirmLabel="Turn off" busy={busy} onCancel={() => setRemoving(false)} onConfirm={() => void remove()}>
      <p>Ledger forgets the token and the activity it synced. Repository links stay.</p>
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
    <p className="muted small">You can approve apps with your Ledger owner login. Once you have a passkey, the approval page accepts only that login, so this password no longer approves apps. To set a new approval password, confirm your owner password below. You do not need the old approval password.</p>
    <form className="password-form" onSubmit={event => void save(event)}>
      <label>Owner password
        <input id="approval-owner-password" type="password" autoComplete="current-password" value={ownerPassword} onChange={event => setOwnerPassword(event.target.value)} required maxLength={4096} disabled={busy} />
      </label>
      <label>New approval password
        <input id="new-approval-password" type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.target.value)} required minLength={12} maxLength={4096} disabled={busy} />
      </label>
      <label>Confirm new approval password
        <input id="confirm-approval-password" type="password" autoComplete="new-password" value={confirmation} onChange={event => setConfirmation(event.target.value)} required minLength={12} maxLength={4096} disabled={busy} />
      </label>
      {error && <p role="alert">{error}</p>}
      {saved && <p role="status">Approval password changed.</p>}
      <div className="form-actions"><button className="btn btn-primary" disabled={busy}>Change approval password</button></div>
    </form>
  </section>
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
  const overview = useResource(api.getOverview, 'overview', 'oauth_client oauth_token')
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
          <li><span>OAuth clients</span><strong>{overview.data.counts.oauth_clients}</strong></li>
          <li><span>Active access tokens</span><strong>{overview.data.counts.active_access_tokens}</strong></li>
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
                <td data-label="Actions" className="actions">
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

function describePasskeyFailure(error: unknown): string {
  if (passkeyCancelled(error)) return 'Passkey setup was cancelled. Nothing was saved.'
  if (error instanceof DOMException) {
    if (error.name === 'InvalidStateError') return 'This password manager or device already holds a Ledger passkey.'
    return 'Your browser could not create the passkey.'
  }
  return describeError(error)
}

/** A wrong password disables the field while it is checked, which drops focus to the page: put it back, selected, to retype. */
function useRefocusOnError(field: RefObject<HTMLInputElement | null>, error: string, busy: boolean) {
  useEffect(() => {
    if (error && !busy) {
      field.current?.focus()
      field.current?.select()
    }
  }, [field, error, busy])
}

/** Passkeys: once one exists, the password alone signs in nowhere, and approving apps needs the owner login. */
function Passkeys({ list }: { list: Resource<{ passkeys: Passkey[]; required: boolean }> }) {
  const [removing, setRemoving] = useState<Passkey | null>(null)
  const passkeys = list.data?.passkeys
  return <section className="connected-apps" aria-labelledby="passkeys-title">
    <h2 id="passkeys-title" className="section-title">Passkeys</h2>
    {passkeys && passkeys.length > 0
      ? <p className="muted small">Signing in takes one of these passkeys, on its own or after your password, in the console and in the Ledger app. Your password alone no longer signs in or approves apps.</p>
      : <p className="muted small">A passkey signs you in with Bitwarden, your phone, or this computer's screen lock, with nothing to type. After you add one, your password alone no longer signs in (here or in the Ledger app) and no longer approves apps. Both then need the passkey too. Bitwarden can store it and sync it to your other devices.</p>}
    {list.loading && <Loading label="Loading passkeys…" />}
    {!list.loading && !list.data && <ErrorState message="Couldn't load passkeys." onRetry={list.reload} />}
    {passkeys && passkeys.length > 0 && (
      <table className="table clients rows-middle">
        <caption className="visually-hidden">Passkeys</caption>
        <thead><tr><th scope="col">Name</th><th scope="col">Provider</th><th scope="col">Added</th><th scope="col">Last used</th><th scope="col"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody>{passkeys.map((passkey) => <PasskeyRow key={passkey.id} passkey={passkey} onChanged={list.reload} onRemove={() => setRemoving(passkey)} />)}</tbody>
      </table>
    )}
    {passkeys && <AddPasskey first={passkeys.length === 0} onAdded={list.reload} />}
    <RemovePasskey passkey={removing} last={passkeys?.length === 1} onClose={() => setRemoving(null)} onRemoved={list.reload} />
  </section>
}

function PasskeyRow({ passkey, onChanged, onRemove }: { passkey: Passkey; onChanged: () => void; onRemove: () => void }) {
  // The name being edited; null while not renaming.
  const [name, setName] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const toast = useToast()

  async function save(event: FormEvent) {
    event.preventDefault()
    const next = name?.trim() ?? ''
    if (busy || !next) return
    setBusy(true)
    try {
      await api.renamePasskey(passkey.id, next)
      setName(null)
      toast(`Renamed passkey to “${next}”.`)
      onChanged()
    } catch (failure) { toast(describeError(failure), 'error') }
    finally { setBusy(false) }
  }

  return <tr>
    <td data-label="Name">{name === null ? passkey.name : (
      <form className="inline-form rename-form" onSubmit={(event) => void save(event)}>
        <input aria-label="Passkey name" value={name} onChange={(event) => setName(event.target.value)} maxLength={60} required autoFocus disabled={busy} />
        <button className="btn btn-small btn-primary" disabled={busy || !name.trim()}>Save</button>
        <button type="button" className="btn btn-small" onClick={() => setName(null)} disabled={busy}>Cancel</button>
      </form>
    )}</td>
    <td data-label="Provider">{passkey.provider || <span className="muted">Unknown</span>}</td>
    <td data-label="Added"><Timestamp iso={passkey.created_at} /></td>
    <td data-label="Last used">{passkey.last_used_at ? <Timestamp iso={passkey.last_used_at} /> : <span className="muted">never</span>}</td>
    <td data-label="Actions" className="actions">{name === null && <span className="row-actions">
      <button type="button" className="btn btn-small" onClick={() => setName(passkey.name)}>Rename</button>
      <button type="button" className="btn btn-small btn-danger-quiet" onClick={onRemove}>Remove</button>
    </span>}</td>
  </tr>
}

/** Adding a passkey needs the owner password as well as the session, so a stolen session cookie cannot add its own. */
function AddPasskey({ first, onAdded }: { first: boolean; onAdded: () => void }) {
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const field = useRef<HTMLInputElement>(null)
  const toast = useToast()
  useRefocusOnError(field, error, busy)

  if (!passkeysSupported()) return <p className="muted small">This browser cannot create passkeys. Add one from a current browser or from the Ledger app.</p>

  async function add(event: FormEvent) {
    event.preventDefault()
    if (busy || !password) return
    setBusy(true)
    setError('')
    try {
      const { challenge_id, options } = await api.passkeyOptions(password)
      const { passkey, revoked, csrf_token } = await api.addPasskey(challenge_id, await createPasskey(options))
      // A session that had only proved the password was given a new cookie (by the browser) and this token.
      if (csrf_token) setCsrfToken(csrf_token)
      setPasskeyUsedHere(true)
      setPassword('')
      setOpen(false)
      // The server signs out the other devices that had signed in with only the password.
      const signedOut = revoked > 0 ? ` Signed out ${revoked === 1 ? '1 other device' : `${revoked} other devices`} that used only the password.` : ''
      toast(`Added passkey “${passkey.name}”.${first ? ' Your password alone no longer signs in.' : ''}${signedOut}`)
      onAdded()
    } catch (failure) { setError(describePasskeyFailure(failure)) }
    finally { setBusy(false) }
  }

  function cancel() {
    setOpen(false)
    setPassword('')
    setError('')
  }

  if (!open) return <button type="button" className={first ? 'btn btn-primary add-passkey' : 'btn add-passkey'} onClick={() => setOpen(true)}><Icon name="passkey" /> Add a passkey</button>
  return <>
    <p className="muted small">Ledger asks for your password so that someone holding only your session cannot add a passkey of their own. Your browser, or Bitwarden, then asks where to save the passkey.</p>
    <form className="inline-form passkey-form" onSubmit={(event) => void add(event)}>
      <label htmlFor="passkey-password">Confirm your owner password</label>
      <input ref={field} id="passkey-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required maxLength={4096} disabled={busy} autoFocus />
      <button className="btn btn-primary" disabled={busy || !password}>{busy ? 'Waiting for your passkey…' : 'Continue'}</button>
      <button type="button" className="btn" onClick={cancel} disabled={busy}>Cancel</button>
    </form>
    {error && <p className="field-error" role="alert">{error}</p>}
  </>
}

function RemovePasskey({ passkey, last, onClose, onRemoved }: { passkey: Passkey | null; last: boolean; onClose: () => void; onRemoved: () => void }) {
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const field = useRef<HTMLInputElement>(null)
  const toast = useToast()
  useRefocusOnError(field, error, busy)

  function close() {
    setPassword('')
    setError('')
    onClose()
  }

  async function remove() {
    if (!passkey) return
    setBusy(true)
    setError('')
    try {
      await api.removePasskey(passkey.id, password)
      toast(`Removed passkey “${passkey.name}”.${last ? ' Your password alone signs in again.' : ''}`)
      close()
      onRemoved()
    } catch (failure) { setError(describeError(failure)) }
    finally { setBusy(false) }
  }

  return <ConfirmDialog open={passkey !== null} title={`Remove passkey “${passkey?.name ?? ''}”?`} confirmLabel="Remove passkey" busy={busy} confirmDisabled={!password} onCancel={close} onConfirm={() => void remove()}>
    <p>This passkey stops working for Ledger. Delete it from Bitwarden or your device as well.</p>
    {last && <p className="notice notice-danger">This is your only passkey. Removing it returns Ledger to password-only sign-in: your password alone signs in and approves apps again.</p>}
    <label>Owner password
      <input ref={field} type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} maxLength={4096} disabled={busy} autoFocus />
    </label>
    {error && <p className="field-error" role="alert">{error}</p>}
  </ConfirmDialog>
}

const METHOD_LABEL: Record<SignInMethod, string> = { password: 'Password', passkey: 'Passkey', 'password+passkey': 'Password and passkey' }

/** Every browser and app signed in to the console, each of which can be signed out from here. */
function SignedInDevices({ passkeysRequired }: { passkeysRequired: boolean }) {
  const sessions = useResource(api.listSessions, 'sessions', 'admin_session')
  const { signOut } = useAuth()
  const [target, setTarget] = useState<DeviceSession | 'others' | null>(null)
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  // This device first; the rest in the server's order.
  const list = sessions.data && [...sessions.data].sort((a, b) => Number(b.current) - Number(a.current))
  const others = list?.filter((session) => !session.current).length ?? 0

  async function confirm() {
    if (!target) return
    setBusy(true)
    try {
      if (target === 'others') {
        const { revoked } = await api.endOtherSessions()
        toast(revoked === 1 ? 'Signed out 1 other device.' : `Signed out ${revoked} other devices.`)
      } else if (target.current) {
        // The same as Sign out in the menu: the console drops to the sign-in page.
        await signOut()
        return
      } else {
        await api.endSession(target.id)
        toast(`Signed out ${target.label}.`)
      }
      setTarget(null)
      sessions.reload()
    } catch (failure) { toast(describeError(failure), 'error') }
    finally { setBusy(false) }
  }

  const title = target === 'others' ? 'Sign out everywhere else?' : target?.current ? 'Sign out this device?' : `Sign out ${target?.label ?? ''}?`
  return <section className="connected-apps" aria-labelledby="devices-title">
    <h2 id="devices-title" className="section-title">Signed-in devices</h2>
    <p className="muted small">Browsers and Ledger apps signed in as you. A remembered device stays signed in until 30 days after its last use, and 90 days at most; any other for 12 hours. Signing a device out takes effect on its next request.</p>
    {sessions.loading && <Loading label="Loading signed-in devices…" />}
    {!sessions.loading && !list && <ErrorState message="Couldn't load signed-in devices." onRetry={sessions.reload} />}
    {list && list.length > 0 && (
      <table className="table clients rows-middle">
        <caption className="visually-hidden">Signed-in devices</caption>
        <thead><tr><th scope="col">Device</th><th scope="col">Signed in with</th><th scope="col">Signed in</th><th scope="col">Last active</th><th scope="col">Expires</th><th scope="col"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody>
          {list.map((session) => (
            <tr key={session.id}>
              <td data-label="Device"><span>{session.label || 'Unknown device'}{session.current && <> <span className="badge" data-current="">This device</span></>}</span></td>
              <td data-label="Signed in with">{METHOD_LABEL[session.method] ?? session.method}
                {/* Adding a passkey keeps the session that added it (other password-only ones are signed out). */}
                {passkeysRequired && session.method === 'password' && <span className="hint cell-hint">Signed in before you added a passkey</span>}</td>
              <td data-label="Signed in"><Timestamp iso={session.created_at} /></td>
              <td data-label="Last active"><Timestamp iso={session.last_seen_at} /></td>
              <td data-label="Expires"><span>{session.remember && 'Remembered · '}<Timestamp iso={session.expires_at} /></span></td>
              <td data-label="Actions" className="actions"><button type="button" className="btn btn-small btn-danger-quiet" onClick={() => setTarget(session)}>Sign out</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    )}
    {others > 0 && <button type="button" className="btn btn-danger-quiet sign-out-others" onClick={() => setTarget('others')}>Sign out everywhere else</button>}
    <ConfirmDialog open={target !== null} title={title} confirmLabel={target === 'others' ? 'Sign out others' : 'Sign out'} busy={busy} onCancel={() => setTarget(null)} onConfirm={() => void confirm()}>
      {target === 'others' && <p>Every other browser and Ledger app signed in as you is signed out and has to sign in again. Agents and connected apps keep their access; cut theirs under Connected apps.</p>}
      {target !== 'others' && target?.current && <p>You return to the sign-in page.</p>}
      {target !== 'others' && target && !target.current && <p>That device returns to the sign-in page on its next request.</p>}
    </ConfirmDialog>
  </section>
}

const NUDGE_KEY = 'ledger.passkey-nudge'

/** A suggestion to add a passkey while none exists and this browser can create one, until dismissed. */
export function PasskeyNudge() {
  const [hidden, setHidden] = useState(() => {
    if (!passkeysSupported()) return true
    try { return localStorage.getItem(NUDGE_KEY) === 'dismissed' } catch { return false }
  })
  if (hidden) return null
  const dismiss = () => {
    try { localStorage.setItem(NUDGE_KEY, 'dismissed') } catch { /* private mode: hidden for this page only */ }
    setHidden(true)
  }
  return <PasskeyNudgeCard onDismiss={dismiss} />
}

function PasskeyNudgeCard({ onDismiss }: { onDismiss: () => void }) {
  const list = useResource(api.listPasskeys, 'passkeys', 'owner_passkey')
  if (!list.data || list.data.passkeys.length > 0) return null
  return <aside className="notice notice-info passkey-nudge" aria-label="Add a passkey">
    <p><strong>Add a passkey.</strong> Sign in with Bitwarden or your device's screen lock, and a leaked password alone no longer gets anyone in.</p>
    <div className="form-actions">
      <Link to="/access" className="btn btn-small btn-primary">Add a passkey</Link>
      <button type="button" className="btn btn-small" onClick={onDismiss}>Not now</button>
    </div>
  </aside>
}
