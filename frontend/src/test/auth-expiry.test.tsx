import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider, useAuth } from '../auth'
import { mockApi } from './helpers'

function AuthProbe() {
  const { state, signIn, signOut } = useAuth()
  return (
    <>
      <span>{state.status}</span>
      <span>{state.status === 'anonymous' ? state.notice ?? 'no-notice' : 'no-notice'}</span>
      <button type="button" onClick={() => void signIn('password')}>
        Sign in
      </button>
      <button type="button" onClick={() => void signOut()}>
        Sign out
      </button>
    </>
  )
}

async function flushAsyncWork() {
  await act(async () => vi.advanceTimersByTimeAsync(0))
}

describe('session expiry', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('locks a bootstrapped session exactly at expiry and clears its token', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-04T00:00:00Z'))
    const { calls } = mockApi({
      'GET /admin/api/session': { body: { authenticated: true, csrf_token: 'csrf-bootstrap', expires_at: '2026-09-04T00:00:01Z' } },
      'POST /admin/api/logout': { status: 204 },
    })
    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    )
    await flushAsyncWork()

    expect(screen.getByText('authenticated')).toBeInTheDocument()
    await act(async () => vi.advanceTimersByTimeAsync(999))
    expect(screen.getByText('authenticated')).toBeInTheDocument()
    await act(async () => vi.advanceTimersByTimeAsync(1))
    expect(screen.getByText('anonymous')).toBeInTheDocument()
    expect(screen.getByText('expired')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^sign out$/i }))
    await flushAsyncWork()
    const expiredLogout = calls.find((call) => call.path === '/admin/api/logout')
    expect(new Headers(expiredLogout?.init.headers).get('X-CSRF-Token')).toBe('')
  })

  it('locks a newly signed-in session at its expiry', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-04T00:00:00Z'))
    mockApi({
      'GET /admin/api/session': { status: 401 },
      'POST /admin/api/login': { body: { csrf_token: 'csrf-login', expires_at: '2026-09-04T00:00:01Z' } },
    })
    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    )
    await flushAsyncWork()

    expect(screen.getByText('anonymous')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^sign in$/i }))
    await flushAsyncWork()
    expect(screen.getByText('authenticated')).toBeInTheDocument()
    await act(async () => vi.advanceTimersByTimeAsync(1000))
    expect(screen.getByText('anonymous')).toBeInTheDocument()
    expect(screen.getByText('expired')).toBeInTheDocument()
  })

  it('does not let a stale expiry callback clear a newer session token', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-04T00:00:00Z'))
    vi.spyOn(window, 'clearTimeout').mockImplementation(() => undefined)
    const { calls } = mockApi({
      'GET /admin/api/session': { body: { authenticated: true, csrf_token: 'csrf-bootstrap', expires_at: '2026-09-04T00:00:01Z' } },
      'POST /admin/api/login': { body: { csrf_token: 'csrf-login', expires_at: '2026-09-04T00:00:02Z' } },
      'POST /admin/api/logout': { status: 204 },
    })
    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    )
    await flushAsyncWork()

    expect(screen.getByText('authenticated')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^sign in$/i }))
    await flushAsyncWork()
    expect(screen.getByText('authenticated')).toBeInTheDocument()

    await act(async () => vi.advanceTimersByTimeAsync(1000))
    expect(screen.getByText('authenticated')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^sign out$/i }))
    await flushAsyncWork()

    const logout = calls.find((call) => call.path === '/admin/api/logout')
    expect(new Headers(logout?.init.headers).get('X-CSRF-Token')).toBe('csrf-login')
  })
})
