import { useState } from 'react'
import { api, describeError, type Client } from '../api'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Loading, StaleNotice, Timestamp } from '../components/ui'
import { useResource } from '../hooks/useResource'

const KIND_LABEL: Record<Client['kind'], string> = { dcr: 'Dynamic registration', cimd: 'Client ID metadata' }

export function ClientsPage() {
  const clients = useResource(() => api.listClients(), 'clients')
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
      clients.reload()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <header className="page-head">
        <h1>OAuth clients</h1>
        <p className="muted">Registered MCP clients and their live credentials. Secrets and token hashes are never shown.</p>
      </header>
      {clients.loading && <Loading label="Loading clients…" />}
      {!clients.loading && !clients.data && <ErrorState message="Couldn't load OAuth clients." onRetry={clients.reload} />}
      {clients.stale && <StaleNotice message="Client list may be out of date." onRetry={clients.reload} />}
      {clients.data && clients.data.length === 0 && (
        <EmptyState>
          <p>No OAuth clients registered.</p>
        </EmptyState>
      )}
      {clients.data && clients.data.length > 0 && (
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
                Active tokens
              </th>
              <th scope="col">
                <span className="visually-hidden">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {clients.data.map((client) => (
              <tr key={client.client_id}>
                <td>{client.client_name || <span className="muted">unnamed</span>}</td>
                <td>{KIND_LABEL[client.kind]}</td>
                <td>
                  <code className="break">{client.client_id}</code>
                </td>
                <td>
                  <ul className="plain">
                    {client.redirect_uris.map((uri) => (
                      <li key={uri}>
                        <code className="break">{uri}</code>
                      </li>
                    ))}
                  </ul>
                </td>
                <td>
                  <Timestamp iso={client.created_at} />
                </td>
                <td>
                  <Timestamp iso={client.last_used_at} />
                </td>
                <td className="num">{client.active_access_tokens}</td>
                <td>
                  <button type="button" className="btn btn-danger-quiet" onClick={() => setTarget(client)}>
                    Revoke tokens
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <ConfirmDialog open={target !== null} title={`Revoke tokens for ${target?.client_name || target?.client_id || ''}?`} confirmLabel="Revoke" busy={busy} onCancel={() => setTarget(null)} onConfirm={() => void revoke()}>
        <p>Every active access and refresh token for this client stops working immediately. The client can authorize again through the normal OAuth flow.</p>
      </ConfirmDialog>
    </>
  )
}
