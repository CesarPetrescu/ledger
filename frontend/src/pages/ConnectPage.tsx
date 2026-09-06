import { useState, type FormEvent } from 'react'
import { api, describeError, type DeviceRequest } from '../api'
import { Timestamp } from '../components/ui'

const permissions: Record<string, string> = {
  'ledger:read': 'Read project memory',
  'ledger:write': 'Add and update project memory',
  'calendar:read': 'Read selected calendars',
  'calendar:write': 'Change selected calendars',
}

export function ConnectPage() {
  const [code, setCode] = useState('')
  const [request, setRequest] = useState<DeviceRequest | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState('')

  async function lookup(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try { setRequest(await api.lookupDevice(code)) }
    catch (failure) { setError(describeError(failure)) }
    finally { setBusy(false) }
  }

  async function decide(action: 'approve' | 'deny') {
    if (!request) return
    setBusy(true)
    setError('')
    try {
      await api.decideDevice(request.user_code, action)
      setResult(action === 'approve' ? 'Machine approved. Return to your terminal to finish connecting.' : 'Connection denied.')
      setRequest(null)
    } catch (failure) { setError(describeError(failure)) }
    finally { setBusy(false) }
  }

  return <>
    <header className="page-head"><h1>Connect a machine</h1><p className="muted">Run <code>ledger connect codex --server {window.location.origin}</code> on the machine you want to connect, then enter its code here.</p></header>
    {error && <p role="alert">{error}</p>}
    {result ? <p role="status">{result}</p> : request ? <section aria-label="Review connection">
      <h2>Approve this machine?</h2>
      <p>Check that <strong>{request.user_code.slice(0, 4)}-{request.user_code.slice(4)}</strong> matches your terminal. Only approve a connection you started.</p>
      <p>Machine label (provided by the requesting client): <strong>{request.client_name || 'Unnamed machine'}</strong></p>
      <p>Requested <Timestamp iso={request.created_at} /> · Expires <Timestamp iso={request.expires_at} /></p>
      <ul>{request.scope.split(' ').map(scope => <li key={scope}>{permissions[scope] ?? scope}</li>)}</ul>
      <div className="form-actions"><button className="btn" disabled={busy} onClick={() => void decide('deny')}>Deny</button><button className="btn btn-primary" disabled={busy} onClick={() => void decide('approve')}>Approve machine</button></div>
    </section> : <form onSubmit={event => void lookup(event)}>
      <label htmlFor="device-code">Connection code</label>
      <input id="device-code" value={code} onChange={event => setCode(event.target.value)} placeholder="ABCD-2345" autoComplete="off" autoCapitalize="characters" spellCheck={false} required maxLength={9} disabled={busy} />
      <div className="form-actions"><button className="btn btn-primary" disabled={busy}>Review connection</button></div>
    </form>}
  </>
}
