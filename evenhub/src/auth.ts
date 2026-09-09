import { normalizeServer } from './config'

const DEVICE_GRANT = 'urn:ietf:params:oauth:grant-type:device_code'
const STORAGE_KEY = 'ledger-glass-session-v1'
const REQUESTED_SCOPE = 'ledger:read'
const REFRESH_EARLY_MS = 60_000

interface StorageBridge {
  getLocalStorage(key: string): Promise<string>
  setLocalStorage(key: string, value: string): Promise<boolean>
}

interface TokenResponse {
  access_token: string
  refresh_token: string
  token_type: string
  expires_in: number
  scope?: string
}

interface StoredSession {
  version: 1
  server: string
  clientId: string
  accessToken: string
  refreshToken: string
  expiresAt: number
  scope: string
}

interface DeviceAuthorizationResponse {
  device_code: string
  user_code: string
  verification_uri: string
  expires_in: number
  interval?: number
}

interface OAuthErrorBody {
  error?: string
  error_description?: string
}

export interface PairingPrompt {
  userCode: string
  verificationUri: string
  expiresAt: number
  scopes?: string[]
}

export type PairingCallback = (prompt: PairingPrompt) => void | Promise<void>

export class OAuthError extends Error {
  constructor(
    public readonly code: string,
    message: string,
    public readonly status?: number,
  ) {
    super(message)
  }
}

export class LedgerAuth {
  readonly server: string
  private session: StoredSession | null | undefined
  private registrationId: string | undefined
  private pairing: AbortController | undefined

  constructor(
    server: string,
    private readonly storage: StorageBridge,
  ) {
    this.server = normalizeServer(server)
  }

  async accessToken(onPairing?: PairingCallback): Promise<string> {
    const session = await this.load()
    if (session && session.expiresAt - Date.now() > REFRESH_EARLY_MS) return session.accessToken

    if (session?.refreshToken) {
      try {
        return (await this.refresh(session)).accessToken
      } catch (error) {
        if (!(error instanceof OAuthError) || error.code !== 'invalid_grant') throw error
        // Reauthorize the same registered device so confirmed retry keys and
        // reader checkpoints survive token revocation. Owner approval is still required.
      }
    }

    return (await this.pair(onPairing)).accessToken
  }

  async refreshNow(onPairing?: PairingCallback): Promise<string> {
    const session = await this.load()
    if (!session) return (await this.pair(onPairing)).accessToken
    try {
      return (await this.refresh(session)).accessToken
    } catch (error) {
      if (!(error instanceof OAuthError) || error.code !== 'invalid_grant') throw error
      return (await this.pair(onPairing, session.scope)).accessToken
    }
  }

  async requireScopes(scopes: string[], onPairing?: PairingCallback): Promise<string> {
    if (scopes.some(scope => !['ledger:read', 'ledger:write', 'calendar:read'].includes(scope))) throw new Error('Unsupported Glass scope')
    await this.accessToken(onPairing)
    const session = await this.load()
    if (!session) throw new Error('No Ledger session')
    const requested = [...new Set([...session.scope.split(/\s+/), ...scopes])].sort().join(' ')
    if (scopes.every(scope => session.scope.split(/\s+/).includes(scope))) return session.accessToken
    return (await this.pair(onPairing, requested)).accessToken
  }

  async clientId(): Promise<string> { return (await this.load())?.clientId ?? '' }
  async grantedScopes(): Promise<string[]> { return (await this.load())?.scope.split(/\s+/) ?? [] }
  cancelPairing(): void { this.pairing?.abort(new DOMException('Pairing cancelled', 'AbortError')) }

  async reconnect(onPairing?: PairingCallback): Promise<string> {
    await this.revokeCurrent()
    return (await this.pair(onPairing)).accessToken
  }

  async revokeCurrent(): Promise<void> {
    const session = await this.load()
    if (session) {
      this.registrationId = session.clientId
      try {
        await this.postForm<unknown>(
          '/oauth/revoke',
          new URLSearchParams({ client_id: session.clientId, token: session.refreshToken }),
        )
      } catch {
        // Reconnect must still recover locally if the server is temporarily unreachable.
      }
    }
    await this.clear()
  }

  async clear(): Promise<void> {
    this.session = null
    if (!await this.storage.setLocalStorage(STORAGE_KEY, '')) throw new Error('Even App refused to clear Ledger credentials')
  }

  private async load(): Promise<StoredSession | null> {
    if (this.session !== undefined) return this.session
    const raw = await this.storage.getLocalStorage(STORAGE_KEY)
    if (!raw) return (this.session = null)

    try {
      const parsed = JSON.parse(raw) as Partial<StoredSession>
      if (
        parsed.version !== 1 ||
        parsed.server !== this.server ||
        typeof parsed.clientId !== 'string' ||
        typeof parsed.accessToken !== 'string' ||
        typeof parsed.refreshToken !== 'string' ||
        typeof parsed.expiresAt !== 'number' || !Number.isFinite(parsed.expiresAt) ||
        typeof parsed.scope !== 'string' || !parsed.scope.split(/\s+/).includes('ledger:read')
      ) {
        await this.clear()
        return null
      }
      this.registrationId = parsed.clientId
      this.session = parsed as StoredSession
      return this.session
    } catch {
      await this.clear()
      return null
    }
  }

  private async refresh(session: StoredSession): Promise<StoredSession> {
    const form = new URLSearchParams({
      grant_type: 'refresh_token',
      client_id: session.clientId,
      refresh_token: session.refreshToken,
    })
    const token = await this.postForm<TokenResponse>('/oauth/token', form)
    return this.saveToken(session.clientId, token, session.scope)
  }

  private async pair(onPairing?: PairingCallback, requested = this.session?.scope ?? REQUESTED_SCOPE): Promise<StoredSession> {
    const controller = new AbortController()
    this.pairing = controller
    try {
      let clientId = this.registrationId ?? this.session?.clientId
      if (!clientId) {
        const registration = await this.fetchJSON<{ client_id: string }>('/oauth/register', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ client_name: 'Ledger Glass', grant_types: [DEVICE_GRANT, 'refresh_token'] }),
        })
        if (!registration.client_id) throw new Error('Ledger returned an empty OAuth client ID')
        clientId = registration.client_id
        this.registrationId = clientId
      }
      const device = await this.postForm<DeviceAuthorizationResponse>('/oauth/device', new URLSearchParams({
        client_id: clientId, scope: requested, resource: `${this.server}/mcp`,
      }))
      this.validateDeviceResponse(device)
      const expiresAt = Date.now() + device.expires_in * 1000
      await onPairing?.({ userCode: device.user_code, verificationUri: device.verification_uri, expiresAt, scopes: requested.split(/\s+/) })
      let waitSeconds = Math.max(1, device.interval ?? 5)
      while (Date.now() < expiresAt) {
        await sleep(Math.min(waitSeconds * 1000, Math.max(0, expiresAt - Date.now())), controller.signal)
        if (Date.now() >= expiresAt) break
        try {
          const token = await this.postForm<TokenResponse>('/oauth/token', new URLSearchParams({
            grant_type: DEVICE_GRANT, client_id: clientId, device_code: device.device_code,
          }))
          return await this.saveToken(clientId, token, requested)
        } catch (error) {
          if (!(error instanceof OAuthError)) throw error
          if (error.code === 'authorization_pending') continue
          if (error.code === 'slow_down') { waitSeconds = Math.min(60, waitSeconds + 5); continue }
          throw error
        }
      }
      throw new OAuthError('expired_token', 'Ledger device approval expired')
    } finally { if (this.pairing === controller) this.pairing = undefined }
  }

  private validateDeviceResponse(device: DeviceAuthorizationResponse): void {
    if (!device.device_code || !device.user_code || !device.verification_uri || !Number.isFinite(device.expires_in) || device.expires_in <= 0 || device.expires_in > 600 ||
        (device.interval !== undefined && (!Number.isFinite(device.interval) || device.interval < 1 || device.interval > 60))) {
      throw new Error('Ledger returned an invalid device authorization response')
    }
    const verificationURL = new URL(device.verification_uri)
    if (verificationURL.origin !== this.server) {
      throw new Error('Ledger returned a cross-origin verification URL')
    }
  }

  private async saveToken(clientId: string, token: TokenResponse, requested = REQUESTED_SCOPE): Promise<StoredSession> {
    if (
      typeof token.access_token !== 'string' || !token.access_token || token.access_token.length > 4096 ||
      typeof token.refresh_token !== 'string' || !token.refresh_token || token.refresh_token.length > 4096 ||
      typeof token.token_type !== 'string' || token.token_type.toLowerCase() !== 'bearer' ||
      !Number.isFinite(token.expires_in) || token.expires_in <= 0 ||
      token.expires_in > 86_400
    ) {
      throw new Error('Ledger returned an invalid OAuth token response')
    }

    if (token.scope !== undefined && typeof token.scope !== 'string') throw new Error('Invalid granted scope')
    const session: StoredSession = {
      version: 1,
      server: this.server,
      clientId,
      accessToken: token.access_token,
      refreshToken: token.refresh_token,
      expiresAt: Date.now() + token.expires_in * 1000,
      scope: token.scope ?? requested,
    }
    if (requested.split(/\s+/).some(scope => !session.scope.split(/\s+/).includes(scope))) {
      throw new Error('Ledger token is missing requested permissions')
    }

    const saved = await this.storage.setLocalStorage(STORAGE_KEY, JSON.stringify(session))
    if (!saved) throw new Error('Even App refused to persist Ledger credentials')
    this.session = session
    return session
  }

  private postForm<T>(path: string, form: URLSearchParams): Promise<T> {
    return this.fetchJSON<T>(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: form.toString(),
    })
  }

  private async fetchJSON<T>(path: string, init: RequestInit): Promise<T> {
    const controller = new AbortController()
    const upstream = this.pairing?.signal
    const cancel = () => controller.abort(upstream?.reason)
    if (upstream?.aborted) cancel()
    else upstream?.addEventListener('abort', cancel, { once: true })
    const timer = setTimeout(() => controller.abort(new DOMException('Ledger OAuth timed out', 'TimeoutError')), 8_000)
    try {
      const response = await fetch(new URL(path, this.server), {
        ...init, signal: controller.signal, credentials: 'omit', redirect: 'error',
        headers: { Accept: 'application/json', ...init.headers },
      })
      const text = await response.text()
      if (text.length > 65_536) throw new Error('Oversized OAuth response')
      let body: unknown = null
      if (text) {
        try { body = JSON.parse(text) }
        catch {
          if (!response.ok) throw new OAuthError(`http_${response.status}`, `Ledger OAuth returned HTTP ${response.status}`, response.status)
          throw new Error('Ledger OAuth returned invalid JSON')
        }
      }
      if (!response.ok) {
        const oauth = (body ?? {}) as OAuthErrorBody
        throw new OAuthError(oauth.error ?? `http_${response.status}`, oauth.error_description ?? `Ledger OAuth returned HTTP ${response.status}`, response.status)
      }
      return body as T
    } finally {
      clearTimeout(timer)
      upstream?.removeEventListener('abort', cancel)
    }
  }
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(signal.reason); return }
    const cancel = () => { clearTimeout(timer); signal.removeEventListener('abort', cancel); reject(signal.reason) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', cancel); resolve() }, ms)
    signal.addEventListener('abort', cancel, { once: true })
  })
}
