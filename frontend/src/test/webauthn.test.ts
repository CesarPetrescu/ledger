import { afterEach, describe, expect, it, vi } from 'vitest'
import { createPasskey, fromBase64url, getPasskey, passkeyCancelled, passkeysSupported, toBase64url } from '../webauthn'
import { stubPasskeys } from './helpers'

const bytes = (...values: number[]) => new Uint8Array(values).buffer

/** A browser without the Level 3 JSON helpers, like older Safari and Firefox. */
function stubOldBrowser(get: unknown, create: unknown) {
  vi.stubGlobal('PublicKeyCredential', class {})
  Object.defineProperty(navigator, 'credentials', { value: { get, create }, configurable: true })
}

afterEach(() => {
  Reflect.deleteProperty(navigator, 'credentials')
})

describe('webauthn helpers', () => {
  it('encodes base64url without padding and decodes it back', () => {
    // 0xfb 0xff yields "+/" in plain base64.
    const data = bytes(0xfb, 0xff, 0x00, 0x10)
    expect(toBase64url(data)).toBe('-_8AEA')
    expect(new Uint8Array(fromBase64url('-_8AEA'))).toEqual(new Uint8Array(data))
    expect(toBase64url(new Uint8Array([1, 2, 3, 4, 5]).subarray(1, 3))).toBe('AgM')
    expect(new Uint8Array(fromBase64url('AgM'))).toEqual(new Uint8Array([2, 3]))
  })

  it('knows when the browser has no passkeys', () => {
    expect(passkeysSupported()).toBe(false)
    stubPasskeys()
    expect(passkeysSupported()).toBe(true)
  })

  it('uses the browser JSON helpers when present', async () => {
    const passkeys = stubPasskeys()
    const options = { challenge: 'AAEC', allowCredentials: [], userVerification: 'required' }
    await expect(getPasskey(options)).resolves.toEqual(passkeys.assertion)
    expect(passkeys.get).toHaveBeenCalledWith({ publicKey: { ...options, parsed: 'request' } })
  })

  it('decodes request options and builds assertion JSON by hand in older browsers', async () => {
    const credential = {
      id: 'AQI', rawId: bytes(1, 2), type: 'public-key', authenticatorAttachment: 'cross-platform',
      response: { clientDataJSON: bytes(3), authenticatorData: bytes(4, 5), signature: bytes(6), userHandle: bytes(7) },
      getClientExtensionResults: () => ({}),
    }
    const get = vi.fn(async () => credential)
    stubOldBrowser(get, vi.fn())
    const json = await getPasskey({ challenge: 'AAEC', allowCredentials: [{ id: 'CQo', type: 'public-key', transports: ['hybrid'] }], userVerification: 'required', rpId: 'ledger.example.com' })
    const publicKey = (get.mock.calls[0] as unknown as [CredentialRequestOptions])[0].publicKey!
    expect(new Uint8Array(publicKey.challenge as ArrayBuffer)).toEqual(new Uint8Array([0, 1, 2]))
    expect(new Uint8Array(publicKey.allowCredentials![0]!.id as ArrayBuffer)).toEqual(new Uint8Array([9, 10]))
    expect(publicKey.userVerification).toBe('required')
    expect(publicKey.rpId).toBe('ledger.example.com')
    expect(json).toEqual({
      id: 'AQI', rawId: 'AQI', type: 'public-key', authenticatorAttachment: 'cross-platform',
      response: { clientDataJSON: 'Aw', authenticatorData: 'BAU', signature: 'Bg', userHandle: 'Bw' },
      clientExtensionResults: {},
    })
  })

  it('falls back to hand-built JSON when a provider credential cannot serialize itself', async () => {
    // Bitwarden's extension returns look-alike objects; the prototype's toJSON can throw on them.
    const credential = {
      id: 'AQI', rawId: bytes(1, 2), type: 'public-key',
      response: { clientDataJSON: bytes(3), attestationObject: bytes(8), getTransports: () => ['internal', 'hybrid'] },
      toJSON: () => { throw new TypeError('Illegal invocation') },
      getClientExtensionResults: () => { throw new TypeError('Illegal invocation') },
    }
    const create = vi.fn(async () => credential)
    stubOldBrowser(vi.fn(), create)
    const json = await createPasskey({
      challenge: 'AAEC',
      rp: { id: 'ledger.example.com', name: 'Ledger' },
      user: { id: 'b3duZXI', name: 'owner', displayName: 'Ledger owner' },
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }, { type: 'public-key', alg: -8 }, { type: 'public-key', alg: -257 }],
      excludeCredentials: [{ id: 'CQo', type: 'public-key' }],
      authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
      attestation: 'none',
    })
    const publicKey = (create.mock.calls[0] as unknown as [CredentialCreationOptions])[0].publicKey!
    expect(new TextDecoder().decode(publicKey.user.id as ArrayBuffer)).toBe('owner')
    expect(new Uint8Array(publicKey.excludeCredentials![0]!.id as ArrayBuffer)).toEqual(new Uint8Array([9, 10]))
    expect(publicKey.authenticatorSelection).toEqual({ residentKey: 'required', userVerification: 'required' })
    expect(json).toEqual({
      id: 'AQI', rawId: 'AQI', type: 'public-key',
      response: { clientDataJSON: 'Aw', attestationObject: 'CA', transports: ['internal', 'hybrid'] },
      clientExtensionResults: {},
    })
  })

  it('treats a closed prompt or an empty answer as cancelled', async () => {
    stubOldBrowser(vi.fn(async () => null), vi.fn(async () => null))
    const failure = await getPasskey({ challenge: 'AAEC' }).catch((error: unknown) => error)
    expect(passkeyCancelled(failure)).toBe(true)
    expect(passkeyCancelled(new DOMException('timed out', 'AbortError'))).toBe(true)
    expect(passkeyCancelled(new DOMException('wrong site', 'SecurityError'))).toBe(false)
    expect(passkeyCancelled(new Error('NotAllowedError'))).toBe(false)
  })
})
