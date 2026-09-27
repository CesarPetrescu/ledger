import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { applyTheme, readTheme } from '../theme'
import { authenticatedSession, mockApi, overview, renderApp } from './helpers'

describe('theme', () => {
  afterEach(() => applyTheme('system'))

  it('follows the system by default and cycles light, dark, then system', async () => {
    window.localStorage.removeItem('ledger-theme')
    applyTheme(readTheme())
    expect(document.documentElement.dataset.theme).toBeUndefined()
    mockApi({ 'GET /admin/api/session': authenticatedSession, 'GET /admin/api/overview': { body: overview } })
    renderApp()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Theme: System. Switch to Light' }))
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(window.localStorage.getItem('ledger-theme')).toBe('light')
    await user.click(screen.getByRole('button', { name: 'Theme: Light. Switch to Dark' }))
    expect(document.documentElement.dataset.theme).toBe('dark')
    await user.click(screen.getByRole('button', { name: 'Theme: Dark. Switch to System' }))
    expect(document.documentElement.dataset.theme).toBeUndefined()
    expect(window.localStorage.getItem('ledger-theme')).toBeNull()
  })
})
