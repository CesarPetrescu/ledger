import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import type { DeviceSession, Passkey } from '../api'
import { authenticatedSession, futureSessionExpiry, homeRoutes, mockApi, renderApp, stubPasskeys } from './helpers'

const bitwarden: Passkey = { id: 'pk-1', name: 'Bitwarden', provider: 'Bitwarden', created_at: '2026-10-01T10:00:00Z', last_used_at: '2026-10-10T10:00:00Z' }
// As the server sends a passkey whose provider it does not know and that was never used.
const pixel: Passkey = { id: 'pk-2', name: 'Pixel', provider: '', created_at: '2026-10-02T10:00:00Z', last_used_at: null }

const creationOptionsJSON = {
  challenge: 'Y2hhbGxlbmdl',
  rp: { id: 'ledger.example.com', name: 'Ledger' },
  user: { id: 'b3duZXI', name: 'owner', displayName: 'Ledger owner' },
  pubKeyCredParams: [{ type: 'public-key', alg: -7 }, { type: 'public-key', alg: -8 }, { type: 'public-key', alg: -257 }],
  excludeCredentials: [],
  authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
  attestation: 'none',
}

function device(id: string, label: string, extra: Partial<DeviceSession> = {}): DeviceSession {
  return { id, label, method: 'passkey', remember: true, created_at: '2026-10-09T10:00:00Z', last_seen_at: '2026-10-10T10:00:00Z', expires_at: futureSessionExpiry(), current: false, ...extra }
}

const here = device('s-here', 'Firefox on Linux', { method: 'password+passkey', current: true })
const phone = device('s-phone', 'Ledger app on Android')
const office = device('s-office', 'Chrome on Windows', { method: 'password', remember: false })

const accessRoutes = {
  'GET /admin/api/session': authenticatedSession,
  'GET /admin/api/oauth/clients': { body: { clients: [] } },
  'GET /admin/api/api-keys': { body: { keys: [] } },
  'GET /admin/api/github-sync': { body: { configured: false } },
}

const notifications = () => screen.getByRole('status', { name: /notifications/i })

describe('passkeys on Access', () => {
  it('adds a first passkey only after the password is confirmed, and says what changes', async () => {
    const passkeys = stubPasskeys()
    const { calls } = mockApi({
      ...accessRoutes,
      'GET /admin/api/sessions': { body: { sessions: [here] } },
      'GET /admin/api/passkeys': [{ body: { passkeys: [], required: false } }, { body: { passkeys: [bitwarden], required: true } }],
      'POST /admin/api/passkeys/options': [
        { status: 403, body: { error: 'Password not accepted.' } },
        { body: { challenge_id: 'reg-1', options: creationOptionsJSON } },
      ],
      'POST /admin/api/passkeys': { status: 201, body: { passkey: bitwarden } },
    })
    renderApp('/admin/access')
    const section = await screen.findByRole('region', { name: 'Passkeys' })
    expect(section).toHaveTextContent(/your password alone stops working/i)
    expect(section).toHaveTextContent(/Bitwarden can store it/i)
    const user = userEvent.setup()
    await user.click(await within(section).findByRole('button', { name: 'Add a passkey' }))
    await user.type(within(section).getByLabelText('Confirm your owner password'), 'wrong')
    await user.click(within(section).getByRole('button', { name: 'Continue' }))
    expect(await within(section).findByRole('alert')).toHaveTextContent('Password not accepted.')
    // A wrong password does not end the session.
    expect(screen.getByRole('heading', { name: 'Access', level: 1 })).toBeInTheDocument()
    expect(passkeys.create).not.toHaveBeenCalled()

    await user.clear(within(section).getByLabelText('Confirm your owner password'))
    await user.type(within(section).getByLabelText('Confirm your owner password'), 'correct horse')
    await user.click(within(section).getByRole('button', { name: 'Continue' }))
    const table = await within(section).findByRole('table', { name: 'Passkeys' })
    expect(table).toHaveTextContent('Bitwarden')
    expect(notifications()).toHaveTextContent('Added Bitwarden. Your password alone no longer signs in.')
    expect(section).toHaveTextContent(/password alone no longer signs in or approves apps/i)
    expect(passkeys.create).toHaveBeenCalledWith({ publicKey: { ...creationOptionsJSON, parsed: 'creation' } })
    const options = calls.filter((call) => call.path === '/admin/api/passkeys/options')
    expect(options.map((call) => call.body)).toEqual([{ password: 'wrong' }, { password: 'correct horse' }])
    expect(new Headers(options[1]?.init.headers).get('X-CSRF-Token')).toBe('csrf-123')
    expect(calls.find((call) => call.method === 'POST' && call.path === '/admin/api/passkeys')?.body).toEqual({ challenge_id: 'reg-1', credential: passkeys.attestation })
  })

  it('keeps the password ready when the passkey prompt is cancelled', async () => {
    const passkeys = stubPasskeys()
    passkeys.create.mockRejectedValueOnce(new DOMException('closed', 'NotAllowedError'))
    const { calls } = mockApi({
      ...accessRoutes,
      'GET /admin/api/sessions': { body: { sessions: [here] } },
      'GET /admin/api/passkeys': { body: { passkeys: [], required: false } },
      'POST /admin/api/passkeys/options': { body: { challenge_id: 'reg-1', options: creationOptionsJSON } },
    })
    renderApp('/admin/access')
    const section = await screen.findByRole('region', { name: 'Passkeys' })
    const user = userEvent.setup()
    await user.click(await within(section).findByRole('button', { name: 'Add a passkey' }))
    await user.type(within(section).getByLabelText('Confirm your owner password'), 'correct horse')
    await user.click(within(section).getByRole('button', { name: 'Continue' }))
    expect(await within(section).findByRole('alert')).toHaveTextContent('Passkey setup was cancelled. Nothing was saved.')
    expect(within(section).getByLabelText('Confirm your owner password')).toHaveValue('correct horse')
    expect(calls.some((call) => call.method === 'POST' && call.path === '/admin/api/passkeys')).toBe(false)
  })

  it('says when this browser cannot create passkeys', async () => {
    mockApi({ ...accessRoutes, 'GET /admin/api/sessions': { body: { sessions: [here] } }, 'GET /admin/api/passkeys': { body: { passkeys: [bitwarden], required: true } } })
    renderApp('/admin/access')
    const section = await screen.findByRole('region', { name: 'Passkeys' })
    expect(await within(section).findByText(/this browser cannot create passkeys/i)).toBeInTheDocument()
    expect(within(section).queryByRole('button', { name: 'Add a passkey' })).not.toBeInTheDocument()
    expect(within(section).getByRole('table', { name: 'Passkeys' })).toHaveTextContent('Bitwarden')
  })

  it('renames and removes passkeys with the password, warning before the last one goes', async () => {
    stubPasskeys()
    const renamed = { ...bitwarden, name: 'Laptop vault' }
    const { calls } = mockApi({
      ...accessRoutes,
      'GET /admin/api/sessions': { body: { sessions: [here] } },
      'GET /admin/api/passkeys': [
        { body: { passkeys: [bitwarden, pixel], required: true } },
        { body: { passkeys: [renamed, pixel], required: true } },
        { body: { passkeys: [renamed], required: true } },
      ],
      'PATCH /admin/api/passkeys/pk-1': { body: { passkey: renamed } },
      'DELETE /admin/api/passkeys/pk-2': { status: 204 },
    })
    renderApp('/admin/access')
    const section = await screen.findByRole('region', { name: 'Passkeys' })
    const table = await within(section).findByRole('table', { name: 'Passkeys' })
    const [first, second] = within(table).getAllByRole('row').slice(1)
    expect(first).toHaveTextContent('Bitwarden')
    expect(second!.querySelector('[data-label="Provider"]')).toHaveTextContent('Unknown')
    expect(second!.querySelector('[data-label="Last used"]')).toHaveTextContent('never')

    const user = userEvent.setup()
    await user.click(within(first!).getByRole('button', { name: 'Rename' }))
    const name = within(first!).getByLabelText('Passkey name')
    expect(name).toHaveValue('Bitwarden')
    await user.clear(name)
    await user.type(name, 'Laptop vault')
    await user.click(within(first!).getByRole('button', { name: 'Save' }))
    expect(await within(table).findByText('Laptop vault')).toBeInTheDocument()
    expect(calls.find((call) => call.method === 'PATCH')?.body).toEqual({ name: 'Laptop vault' })

    await user.click(within(within(table).getAllByRole('row')[2]!).getByRole('button', { name: 'Remove' }))
    let dialog = await screen.findByRole('dialog', { name: 'Remove Pixel?' })
    expect(dialog).not.toHaveTextContent(/only passkey/i)
    expect(within(dialog).getByRole('button', { name: 'Remove passkey' })).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Owner password'), 'correct horse')
    await user.click(within(dialog).getByRole('button', { name: 'Remove passkey' }))
    await waitFor(() => expect(within(table).queryByText('Pixel')).not.toBeInTheDocument())
    expect(calls.find((call) => call.method === 'DELETE')?.body).toEqual({ password: 'correct horse' })

    await user.click(within(table).getByRole('button', { name: 'Remove' }))
    dialog = await screen.findByRole('dialog', { name: 'Remove Laptop vault?' })
    expect(dialog).toHaveTextContent(/this is your only passkey/i)
    expect(dialog).toHaveTextContent(/password-only sign-in/i)
    // The password typed for the previous removal is not kept.
    expect(within(dialog).getByLabelText('Owner password')).toHaveValue('')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(calls.filter((call) => call.method === 'DELETE')).toHaveLength(1)
  })
})

describe('signed-in devices on Access', () => {
  it('lists devices with this one first and signs them out one by one or all at once', async () => {
    const { calls } = mockApi({
      ...accessRoutes,
      'GET /admin/api/passkeys': { body: { passkeys: [bitwarden], required: true } },
      'GET /admin/api/sessions': [{ body: { sessions: [phone, here, office] } }, { body: { sessions: [here, office] } }, { body: { sessions: [here] } }],
      'DELETE /admin/api/sessions/s-phone': { status: 204 },
      'POST /admin/api/sessions/revoke-others': { body: { revoked: 1 } },
      'POST /admin/api/logout': { status: 204 },
    })
    renderApp('/admin/access')
    const section = await screen.findByRole('region', { name: 'Signed-in devices' })
    const table = await within(section).findByRole('table', { name: 'Signed-in devices' })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows.map((row) => row.querySelector('[data-label="Device"]')?.textContent)).toEqual(['Firefox on Linux This device', 'Ledger app on Android', 'Chrome on Windows'])
    expect(rows[0]).toHaveTextContent('Password and passkey')
    expect(rows[0]!.querySelector('[data-label="Expires"]')).toHaveTextContent(/^Remembered · /)
    expect(rows[2]).toHaveTextContent('Password')
    expect(rows[2]!.querySelector('[data-label="Expires"]')).not.toHaveTextContent('Remembered')

    const user = userEvent.setup()
    await user.click(within(rows[1]!).getByRole('button', { name: 'Sign out' }))
    let dialog = await screen.findByRole('dialog', { name: 'Sign out Ledger app on Android?' })
    await user.click(within(dialog).getByRole('button', { name: 'Sign out' }))
    expect(await within(table).findByText('Chrome on Windows')).toBeInTheDocument()
    await waitFor(() => expect(within(table).queryByText('Ledger app on Android')).not.toBeInTheDocument())
    expect(notifications()).toHaveTextContent('Signed out Ledger app on Android.')
    expect(new Headers(calls.find((call) => call.method === 'DELETE')?.init.headers).get('X-CSRF-Token')).toBe('csrf-123')

    await user.click(within(section).getByRole('button', { name: 'Sign out everywhere else' }))
    dialog = await screen.findByRole('dialog', { name: 'Sign out everywhere else?' })
    expect(dialog).toHaveTextContent(/agents and connected apps keep their access/i)
    await user.click(within(dialog).getByRole('button', { name: 'Sign out others' }))
    await waitFor(() => expect(within(table).queryByText('Chrome on Windows')).not.toBeInTheDocument())
    expect(notifications()).toHaveTextContent('Signed out 1 other device.')
    expect(within(section).queryByRole('button', { name: 'Sign out everywhere else' })).not.toBeInTheDocument()

    await user.click(within(table).getByRole('button', { name: 'Sign out' }))
    dialog = await screen.findByRole('dialog', { name: 'Sign out this device?' })
    await user.click(within(dialog).getByRole('button', { name: 'Sign out' }))
    expect(await screen.findByRole('heading', { name: /welcome back/i })).toBeInTheDocument()
    expect(screen.getByText('Signed out.')).toBeInTheDocument()
    expect(calls.some((call) => call.path === '/admin/api/logout')).toBe(true)
  })
})

describe('passkey nudge', () => {
  const key = 'ledger.passkey-nudge'
  afterEach(() => localStorage.removeItem(key))

  it('suggests a passkey on the Inbox until one exists or it is dismissed', async () => {
    stubPasskeys()
    mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes, 'GET /admin/api/passkeys': { body: { passkeys: [], required: false } } })
    renderApp()
    const nudge = await screen.findByRole('complementary', { name: 'Add a passkey' })
    expect(within(nudge).getByRole('link', { name: 'Add a passkey' })).toHaveAttribute('href', '/admin/access')
    await userEvent.setup().click(within(nudge).getByRole('button', { name: 'Not now' }))
    expect(screen.queryByRole('complementary', { name: 'Add a passkey' })).not.toBeInTheDocument()
    expect(localStorage.getItem(key)).toBe('dismissed')

    // Once dismissed it stays away, without asking the server.
    cleanup()
    const { calls } = mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes })
    renderApp()
    expect(await screen.findByRole('heading', { name: /^inbox$/i })).toBeInTheDocument()
    expect(calls.some((call) => call.path === '/admin/api/passkeys')).toBe(false)
  })

  it('stays away once a passkey exists, or where the browser cannot add one', async () => {
    stubPasskeys()
    const { calls } = mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes, 'GET /admin/api/passkeys': { body: { passkeys: [bitwarden], required: true } } })
    renderApp()
    await screen.findByRole('heading', { name: /^inbox$/i })
    await waitFor(() => expect(calls.some((call) => call.path === '/admin/api/passkeys')).toBe(true))
    expect(screen.queryByRole('complementary', { name: 'Add a passkey' })).not.toBeInTheDocument()

    cleanup()
    Reflect.deleteProperty(navigator, 'credentials')
    const unsupported = mockApi({ 'GET /admin/api/session': authenticatedSession, ...homeRoutes })
    renderApp()
    await screen.findByRole('heading', { name: /^inbox$/i })
    expect(unsupported.calls.some((call) => call.path === '/admin/api/passkeys')).toBe(false)
  })
})
