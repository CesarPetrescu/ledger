// WebAuthn ceremonies in the JSON form the server speaks (WebAuthn Level 3, base64url fields).
// The browser's own JSON helpers are used when present; older browsers, and passkey providers
// such as Bitwarden's extension whose credential objects are not real platform objects, get
// the same JSON built by hand.

/** A credential as PublicKeyCredential.toJSON() returns it, ready to send. */
export type CredentialJSON = Record<string, unknown>

export function passkeysSupported(): boolean {
  return typeof window.PublicKeyCredential === 'function' && typeof navigator.credentials?.get === 'function'
}

/** The person closed or timed out the prompt; nothing reached the server. */
export function passkeyCancelled(error: unknown): boolean {
  return error instanceof DOMException && (error.name === 'NotAllowedError' || error.name === 'AbortError')
}

export function toBase64url(data: ArrayBuffer | ArrayBufferView): string {
  const bytes = ArrayBuffer.isView(data) ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength) : new Uint8Array(data)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function fromBase64url(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const binary = atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, '='))
  return Uint8Array.from(binary, (char) => char.charCodeAt(0)).buffer
}

/** Reads an optional attribute that a provider's look-alike credential may not implement. */
function optional<T>(read: () => T | null | undefined, fallback: T): T {
  try {
    return read() ?? fallback
  } catch {
    return fallback
  }
}

function descriptors(list: PublicKeyCredentialDescriptorJSON[]): PublicKeyCredentialDescriptor[] {
  return list.map((item) => ({ ...item, id: fromBase64url(item.id) }) as PublicKeyCredentialDescriptor)
}

// The fallbacks decode the binary fields Ledger sends. Extensions pass through untouched: Ledger asks for
// none that carry binary data.
function requestOptions(json: PublicKeyCredentialRequestOptionsJSON): PublicKeyCredentialRequestOptions {
  if (typeof PublicKeyCredential.parseRequestOptionsFromJSON === 'function') return PublicKeyCredential.parseRequestOptionsFromJSON(json)
  const { challenge, allowCredentials, ...rest } = json
  const options = { ...rest, challenge: fromBase64url(challenge) } as unknown as PublicKeyCredentialRequestOptions
  if (allowCredentials) options.allowCredentials = descriptors(allowCredentials)
  return options
}

function creationOptions(json: PublicKeyCredentialCreationOptionsJSON): PublicKeyCredentialCreationOptions {
  if (typeof PublicKeyCredential.parseCreationOptionsFromJSON === 'function') return PublicKeyCredential.parseCreationOptionsFromJSON(json)
  const { challenge, user, excludeCredentials, ...rest } = json
  const options = { ...rest, challenge: fromBase64url(challenge), user: { ...user, id: fromBase64url(user.id) } } as unknown as PublicKeyCredentialCreationOptions
  if (excludeCredentials) options.excludeCredentials = descriptors(excludeCredentials)
  return options
}

export function credentialToJSON(credential: PublicKeyCredential): CredentialJSON {
  try {
    if (typeof credential.toJSON === 'function') return credential.toJSON() as CredentialJSON
  } catch {
    // A provider's look-alike object can carry the prototype's toJSON without the internals it reads.
  }
  const response = credential.response as AuthenticatorResponse & Partial<AuthenticatorAttestationResponse> & Partial<AuthenticatorAssertionResponse>
  const fields: Record<string, unknown> = { clientDataJSON: toBase64url(response.clientDataJSON) }
  if (response.attestationObject) {
    fields.attestationObject = toBase64url(response.attestationObject)
    fields.transports = optional(() => response.getTransports?.(), [])
  } else {
    fields.authenticatorData = toBase64url(response.authenticatorData!)
    fields.signature = toBase64url(response.signature!)
    fields.userHandle = response.userHandle ? toBase64url(response.userHandle) : null
  }
  const json: CredentialJSON = {
    id: credential.id,
    rawId: toBase64url(credential.rawId),
    type: credential.type,
    response: fields,
    clientExtensionResults: optional(() => credential.getClientExtensionResults(), {}),
  }
  const attachment = optional(() => credential.authenticatorAttachment, null)
  if (attachment) json.authenticatorAttachment = attachment
  return json
}

function cancelled(): DOMException {
  return new DOMException('The passkey prompt closed.', 'NotAllowedError')
}

/** Asks the browser (or Bitwarden) for a passkey assertion. Aborting [signal] closes a prompt left open. */
export async function getPasskey(options: PublicKeyCredentialRequestOptionsJSON, signal?: AbortSignal): Promise<CredentialJSON> {
  const credential = await navigator.credentials.get(signal ? { publicKey: requestOptions(options), signal } : { publicKey: requestOptions(options) })
  if (!credential) throw cancelled()
  return credentialToJSON(credential as PublicKeyCredential)
}

/** Asks the browser (or Bitwarden) to create and store a new passkey. */
export async function createPasskey(options: PublicKeyCredentialCreationOptionsJSON): Promise<CredentialJSON> {
  const credential = await navigator.credentials.create({ publicKey: creationOptions(options) })
  if (!credential) throw cancelled()
  return credentialToJSON(credential as PublicKeyCredential)
}

const USED_HERE = 'ledger.passkey-used'

/** Whether this browser has signed in with, or added, a Ledger passkey: the sign-in page then leads with the passkey. */
export function passkeyUsedHere(): boolean {
  try {
    return localStorage.getItem(USED_HERE) === 'yes'
  } catch {
    return false
  }
}

export function setPasskeyUsedHere(used: boolean): void {
  try {
    if (used) localStorage.setItem(USED_HERE, 'yes')
    else localStorage.removeItem(USED_HERE)
  } catch {
    // Private mode: the password stays the primary way in on this browser.
  }
}
