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
    await api.login('secret')
    expect(Object.keys(window.localStorage)).toHaveLength(0)
    expect(Object.keys(window.sessionStorage)).toHaveLength(0)
    expect(document.cookie).toBe('')
  })
})
