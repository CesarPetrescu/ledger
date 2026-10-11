import { describe, expect, it, vi } from 'vitest'
import { ApiError, UnauthorizedError, api, onUnauthorized, setCsrfToken } from '../api'
import { mockApi } from './helpers'

describe('api client', () => {
  it('sends the CSRF token only on state-changing requests and keeps cookies same-origin', async () => {
    const { calls } = mockApi({
      'GET /admin/api/projects': { body: { projects: [] } },
      'POST /admin/api/projects/atlas/entries': { status: 201, body: { id: 1 } },
    })
    setCsrfToken('csrf-xyz')
    await api.listProjects()
    await api.appendEntry('atlas', 'note', 'hello')
    const [get, post] = calls
    expect(get?.init.credentials).toBe('same-origin')
    expect(new Headers(get?.init.headers).get('X-CSRF-Token')).toBeNull()
    expect(new Headers(post?.init.headers).get('X-CSRF-Token')).toBe('csrf-xyz')
    expect(new Headers(post?.init.headers).get('Content-Type')).toBe('application/json')
    expect(post?.body).toEqual({ kind: 'note', body: 'hello' })
    expect(post?.init.credentials).toBe('same-origin')
  })

  it('refreshes a CSRF token replaced by signing in again in another tab, and retries once', async () => {
    const { calls } = mockApi({
      'POST /admin/api/projects/atlas/entries': [{ status: 403, body: { error: 'invalid csrf token' } }, { status: 201, body: { id: 1 } }],
      'GET /admin/api/session': { body: { authenticated: true, csrf_token: 'csrf-new', expires_at: '2026-09-04T20:00:00Z' } },
    })
    setCsrfToken('csrf-old')
    await expect(api.appendEntry('atlas', 'note', 'hello')).resolves.toEqual({ id: 1 })
    expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual(['POST /admin/api/projects/atlas/entries', 'GET /admin/api/session', 'POST /admin/api/projects/atlas/entries'])
    expect(new Headers(calls[2]?.init.headers).get('X-CSRF-Token')).toBe('csrf-new')
    expect(calls[2]?.body).toEqual({ kind: 'note', body: 'hello' })
  })

  it('retries a CSRF refusal only once, and never an Origin refusal', async () => {
    const { calls } = mockApi({
      'POST /admin/api/projects/atlas/entries': { status: 403, body: { error: 'invalid csrf token' } },
      'POST /admin/api/logout': { status: 403, body: { error: 'origin not allowed' } },
      'GET /admin/api/session': { body: { authenticated: true, csrf_token: 'csrf-new', expires_at: '2026-09-04T20:00:00Z' } },
    })
    await expect(api.appendEntry('atlas', 'note', 'hello')).rejects.toMatchObject({ status: 403 })
    expect(calls).toHaveLength(3)
    await expect(api.logout()).rejects.toMatchObject({ status: 403, message: 'origin not allowed' })
    expect(calls).toHaveLength(4)
  })

  it('turns 401 into UnauthorizedError and notifies the listener', async () => {
    mockApi({ 'GET /admin/api/overview': { status: 401, body: { error: 'unauthenticated' } } })
    const listener = vi.fn()
    const stop = onUnauthorized(listener)
    await expect(api.getOverview()).rejects.toBeInstanceOf(UnauthorizedError)
    expect(listener).toHaveBeenCalledTimes(1)
    stop()
  })

  it('surfaces server validation messages and hides raw failures', async () => {
    mockApi({
      'PUT /admin/api/projects/atlas': { status: 400, body: { error: 'tier must be one of focus, maintain, park' } },
      'GET /admin/api/oauth/clients': () => ({ status: 500, body: '<html>boom</html>' }),
    })
    await expect(api.saveProject('atlas', { name: 'Atlas', tier: 'urgent', hours_wk: 1 })).rejects.toMatchObject({ status: 400, message: 'tier must be one of focus, maintain, park' })
    const failure = await api.listClients().catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(ApiError)
    expect((failure as ApiError).message).toBe('The server could not complete the request.')
    expect((failure as ApiError).message).not.toContain('boom')
  })

  it('never stores authentication material in web storage', async () => {
    mockApi({ 'POST /admin/api/login': { body: { csrf_token: 'csrf-login', expires_at: '2026-09-04T20:00:00Z' } } })
    await api.login('secret', true)
    expect(Object.keys(window.localStorage)).toHaveLength(0)
    expect(Object.keys(window.sessionStorage)).toHaveLength(0)
    expect(document.cookie).toBe('')
  })
})
