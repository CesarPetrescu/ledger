import assert from 'node:assert/strict'
import { beforeAll, afterAll, describe, it } from 'vitest'
import { LedgerAuth } from '../src/auth'

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

describe.sequential('Real OAuth with only the host-storage boundary doubled', () => {
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
    assert(current.clientId !== previous.clientId, 'Revoked identity was reused')
  })
  it('rejects a host refusal to persist a freshly issued real grant', async () => {
    const refusing = new HostStorageDouble()
    refusing.refuseWrites = true
    await assert.rejects(() => new LedgerAuth(server, refusing).accessToken(approve), /refused to persist/)
    assert.equal(refusing.values.size, 0)
  })
})
