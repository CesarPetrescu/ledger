import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client'
import { createDeadlineFetch } from './network'
import type { CaptureDraft, ChangePage, EntrySource, SearchResult, CalendarEvent } from './features'
import type { ProjectDetail, ProjectListResult, ProjectTier } from './types'

export class LedgerMCP {
  private client: Client | null = null
  private transport: StreamableHTTPClientTransport | null = null
  private token = ''
  private fetchTimeout = 8_000

  constructor(private readonly server: string) {}

  async listProjects(accessToken: string, tier?: ProjectTier): Promise<ProjectListResult> {
    return this.call<ProjectListResult>(accessToken, 'list_projects', tier ? { tier } : {})
  }

  async getProject(accessToken: string, slug: string, entries = 5): Promise<ProjectDetail> {
    return this.call<ProjectDetail>(accessToken, 'get_project', { slug, entries })
  }

  async append(token: string, draft: CaptureDraft): Promise<{ id: number | string; created_at: string }> {
    return this.call(token, 'append_entry', { slug: draft.slug, kind: draft.kind, body: draft.body, idempotency_key: draft.key })
  }
  async search(token: string, query: string): Promise<SearchResult> { return this.call(token, 'search', { q: query, limit: 10 }) }
  async entry(token: string, id: string): Promise<EntrySource> { return this.call(token, 'get_entry', { id }) }
  async changes(token: string, after?: string, through?: string): Promise<ChangePage> {
    return this.call(token, 'list_changes', { reader: 'glass', limit: 18, ...(after ? { after } : {}), ...(through ? { through } : {}) })
  }
  async acknowledge(token: string, through: string): Promise<{ checkpoint: string }> { return this.call(token, 'ack_changes', { reader: 'glass', through }) }
  async events(token: string, start: string, end: string): Promise<{ events: CalendarEvent[] }> { return this.call(token, 'list_calendar_events', { start, end }) }
  async transcribe(token: string, wav: string, language = '', signal?: AbortSignal): Promise<{ text: string }> { return this.call(token, 'transcribe_audio', { wav_base64: wav, language }, 30_000, signal) }

  async close(): Promise<void> {
    const client = this.client
    const transport = this.transport
    this.client = null
    this.transport = null
    this.token = ''

    try {
      await client?.close()
    } catch {
      // Best effort during reconnect/exit.
    }
    try {
      await transport?.terminateSession()
    } catch {
      // Stateless Ledger normally has no session to terminate.
    }
  }

  private async call<T>(accessToken: string, name: string, args: Record<string, unknown>, timeout = 12_000, signal?: AbortSignal): Promise<T> {
    await this.connect(accessToken)
    if (!this.client) throw new Error('Ledger MCP client is not connected')

    this.fetchTimeout = name === 'transcribe_audio' ? 28_000 : 8_000
    try {
      const result = await this.client.callTool({ name, arguments: args }, { timeout, signal })
      return parseToolResult<T>(result)
    } finally { this.fetchTimeout = 8_000 }
  }

  private async connect(accessToken: string): Promise<void> {
    if (this.client && this.token === accessToken) return
    await this.close()

    const transport = new StreamableHTTPClientTransport(new URL(`${this.server}/mcp`), {
      fetch: (input, init) => createDeadlineFetch(this.fetchTimeout)(input, init),
      requestInit: {
        credentials: 'omit',
        redirect: 'error',
        headers: {
          Authorization: `Bearer ${accessToken}`,
        },
      },
    })
    // The Go server is stateless. The legacy initialize-only identity is lost
    // between requests; modern MCP carries clientInfo in every request.
    const client = new Client({ name: 'ledger-glass', version: '0.1.0' }, {
      versionNegotiation: { mode: { pin: '2026-07-28' } },
    })
    try {
      await client.connect(transport, { timeout: 12_000 })
    } catch (error) {
      await client.close().catch(() => {})
      throw error
    }

    this.transport = transport
    this.client = client
    this.token = accessToken
  }
}

export function parseToolResult<T>(result: unknown): T {
  if (!result || typeof result !== 'object') throw new Error('Ledger MCP returned an empty tool result')
  const value = result as { structuredContent?: unknown; content?: Array<{ type?: string; text?: string }>; isError?: boolean }
  if (value.isError) throw new Error(toolText(value.content) || 'Ledger MCP tool returned an error')
  if (value.structuredContent && typeof value.structuredContent === 'object') return value.structuredContent as T

  const text = toolText(value.content)
  if (text) {
    try {
      return JSON.parse(text) as T
    } catch {
      throw new Error('Ledger MCP returned non-JSON text without structured content')
    }
  }
  throw new Error('Ledger MCP returned no structured content')
}

function toolText(content: Array<{ type?: string; text?: string }> | undefined): string {
  return content?.find(item => item.type === 'text' && typeof item.text === 'string')?.text ?? ''
}
