import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client'
import { createDeadlineFetch } from './network'
import type { ProjectDetail, ProjectListResult, ProjectTier } from './types'

export class LedgerMCP {
  private client: Client | null = null
  private transport: StreamableHTTPClientTransport | null = null
  private token = ''

  constructor(private readonly server: string) {}

  async listProjects(accessToken: string, tier?: ProjectTier): Promise<ProjectListResult> {
    return this.call<ProjectListResult>(accessToken, 'list_projects', tier ? { tier } : {})
  }

  async getProject(accessToken: string, slug: string, entries = 5): Promise<ProjectDetail> {
    return this.call<ProjectDetail>(accessToken, 'get_project', { slug, entries })
  }

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

  private async call<T>(accessToken: string, name: string, args: Record<string, unknown>): Promise<T> {
    await this.connect(accessToken)
    if (!this.client) throw new Error('Ledger MCP client is not connected')

    const result = await this.client.callTool({ name, arguments: args }, { timeout: 12_000 })
    return parseToolResult<T>(result)
  }

  private async connect(accessToken: string): Promise<void> {
    if (this.client && this.token === accessToken) return
    await this.close()

    const transport = new StreamableHTTPClientTransport(new URL(`${this.server}/mcp`), {
      fetch: createDeadlineFetch(),
      requestInit: {
        credentials: 'omit',
        redirect: 'error',
        headers: {
          Authorization: `Bearer ${accessToken}`,
        },
      },
    })
    const client = new Client({ name: 'ledger-glass', version: '0.1.0' })
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
