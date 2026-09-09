import assert from 'node:assert/strict'
import { beforeAll, afterAll, describe, it } from 'vitest'
import { LedgerAuth } from '../src/auth'
import { LedgerMCP } from '../src/ledger'

// ONLY TEST DOUBLE: the two Even host-storage methods. Every OAuth/admin
// request below uses the actual disposable Ledger stack and database.
class HostStorageDouble {
  values = new Map()
  refuseWrites = false
  async getLocalStorage(key) { return this.values.get(key) ?? '' }
  async setLocalStorage(key, value) {
    if (this.refuseWrites) return false
    this.values.set(key, value)
    return true
  }
}
const server = process.env.LEDGER_PUBLIC_URL
assert(server === 'https://localhost:8443' && process.env.GLASS_OWNER_PASSWORD, 'Run only against the system suite disposable deployment')
const key = 'ledger-glass-session-v1'
let cookie, csrf, originalClients
async function owner(path, method = 'GET', body) {
  const response = await fetch(`${server}/admin/api${path}`, {
    method, redirect: 'error', signal: AbortSignal.timeout(10000),
    headers: { Cookie: cookie, Origin: server, 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  assert(response.ok, `Owner API ${path}: ${response.status}`)
  return response.status === 204 ? null : response.json()
}
async function approve(prompt) {
  await owner('/oauth/device', 'POST', { user_code: prompt.userCode, action: 'approve' })
}
beforeAll(async () => {
  const response = await fetch(`${server}/admin/api/login`, {
    method: 'POST', redirect: 'error', signal: AbortSignal.timeout(10000),
    headers: { Origin: server, 'Content-Type': 'application/json' },
    body: JSON.stringify({ password: process.env.GLASS_OWNER_PASSWORD }),
  })
  assert.equal(response.status, 200)
  cookie = response.headers.getSetCookie().map(value => value.split(';')[0]).join('; ')
  csrf = (await response.json()).csrf_token
  assert(cookie && csrf, 'Real owner login did not return a session')
  originalClients = new Set((await owner('/oauth/clients')).clients.map(c => c.client_id))
})
afterAll(async () => {
  if (!originalClients) return
  for (const client of (await owner('/oauth/clients')).clients) {
    if (!originalClients.has(client.client_id)) await owner('/oauth/revoke', 'POST', { client_id: client.client_id })
  }
})

describe('Real OAuth with only the host-storage boundary doubled', () => {
  const storage = new HostStorageDouble()
  let firstToken
  it('reuses a real grant after reconstructing the application client', async () => {
    firstToken = await new LedgerAuth(server, storage).accessToken(approve)
    assert(firstToken, 'Real OAuth did not issue an access token')
    const count = (await owner('/oauth/clients')).clients.length
    const restored = await new LedgerAuth(server, storage).accessToken(() => { throw new Error('Stored session unexpectedly prompted for approval') })
    // Do not use equality assertions that print token values on failure.
    assert(restored === firstToken, 'Stored access token was not reused')
    assert.equal((await owner('/oauth/clients')).clients.length, count)
  })
  it('rotates expired access credentials through the real token endpoint', async () => {
    const old = JSON.parse(await storage.getLocalStorage(key))
    old.expiresAt = 0
    await storage.setLocalStorage(key, JSON.stringify(old))
    const refreshed = await new LedgerAuth(server, storage).accessToken(() => { throw new Error('Refresh incorrectly requested owner approval') })
    const saved = JSON.parse(await storage.getLocalStorage(key))
    assert(refreshed !== firstToken, 'Real refresh did not rotate the access token')
    assert(saved.refreshToken !== old.refreshToken, 'Rotating refresh token was not persisted')
    assert(saved.accessToken === refreshed && saved.expiresAt > Date.now(), 'Rotated session was not persisted coherently')
    assert.equal(saved.scope, 'ledger:read')
  })
  it('requires real approval after the owner revokes the restored grant', async () => {
    const previous = JSON.parse(await storage.getLocalStorage(key))
    await owner('/oauth/revoke', 'POST', { client_id: previous.clientId })
    let approvals = 0
    await new LedgerAuth(server, storage).refreshNow(async prompt => { approvals++; await approve(prompt) })
    const current = JSON.parse(await storage.getLocalStorage(key))
    assert.equal(approvals, 1)
    assert(current.clientId === previous.clientId, 'Device identity changed and would orphan pending receipts/checkpoints')
  })
  it('upgrades permissions only after explicit approval, without changing device identity', async () => {
    const previous = JSON.parse(await storage.getLocalStorage(key))
    let approvals = 0
    const auth = new LedgerAuth(server, storage)
    await auth.requireScopes(['ledger:write', 'calendar:read'], async prompt => {
      approvals++
      assert.deepEqual([...prompt.scopes].sort(), ['calendar:read','ledger:read','ledger:write'])
      await approve(prompt)
    })
    const current = JSON.parse(await storage.getLocalStorage(key))
    assert(current.clientId === previous.clientId)
    assert.equal(approvals, 1)
    await auth.requireScopes(['ledger:write'], () => { throw new Error('Existing grant prompted again') })
  })
  it('preserves real SDK client identity across stateless MCP writes and retries', async () => {
    await owner('/projects/ci-contract', 'PUT', { name: 'Protocol contract', tier: 'park', hours_wk: 0 })
    const auth = new LedgerAuth(server, storage)
    const token = await auth.accessToken(approve)
    const client = new LedgerMCP(server)
    try {
      const draft = { key: 'protocol-contract-0001', slug: 'ci-contract', kind: 'note', body: 'Real stateless TypeScript client identity', confirmed: true, clientId: await auth.clientId() }
      const first = await client.append(token, draft)
      const retry = await client.append(token, draft)
      assert.equal(String(first.id), String(retry.id))
      const entry = await client.entry(token, String(first.id))
      assert.equal(entry.source, 'ledger-glass')
      assert.equal(entry.body, draft.body)
      assert.equal((await owner('/projects/ci-contract')).entries.length, 1)
    } finally { await client.close() }
  })
  it('retains read-only access when an owner denies a write-scope upgrade', async () => {
    const readStorage = new HostStorageDouble()
    const auth = new LedgerAuth(server, readStorage)
    await auth.accessToken(approve)
    const identity = await auth.clientId()
    await assert.rejects(() => auth.requireScopes(['ledger:write'], async prompt => {
      await owner('/oauth/device', 'POST', { user_code: prompt.userCode, action: 'deny' })
    }), error => error.code === 'access_denied')
    assert.equal(await auth.clientId(),identity)
    assert.deepEqual(await auth.grantedScopes(),['ledger:read'])
    const token = await auth.accessToken(() => { throw new Error('Denial destroyed the prior read grant') })
    const client = new LedgerMCP(server)
    try {
      assert((await client.listProjects(token)).projects.length > 0)
      await assert.rejects(() => client.append(token,{ key:'denied-write-0001',slug:'ci-contract',kind:'note',body:'Never written',confirmed:true,clientId:identity }), /insufficient_scope/)
      assert.equal((await owner('/projects/ci-contract')).entries.length,1)
    } finally { await client.close() }
  })
  it('rejects a host refusal to persist a freshly issued real grant', async () => {
    const refusing = new HostStorageDouble()
    refusing.refuseWrites = true
    await assert.rejects(() => new LedgerAuth(server, refusing).accessToken(approve), /refused to persist/)
    assert.equal(refusing.values.size, 0)
  })
})
