import { describe, expect, it } from 'vitest'
import { parseToolResult } from './ledger'

describe('parseToolResult', () => {
  it('prefers MCP structured content', () => {
    expect(parseToolResult<{ value: number }>({ structuredContent: { value: 7 } })).toEqual({ value: 7 })
  })

  it('falls back to JSON text content', () => {
    expect(parseToolResult<{ ok: boolean }>({ content: [{ type: 'text', text: '{"ok":true}' }] })).toEqual({ ok: true })
  })

  it('surfaces tool errors', () => {
    expect(() => parseToolResult({ isError: true, content: [{ type: 'text', text: 'denied' }] })).toThrow('denied')
  })
})
