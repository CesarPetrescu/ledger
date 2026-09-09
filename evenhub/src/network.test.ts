import { createServer, type Server } from 'node:http'
import { afterEach, describe, expect, it } from 'vitest'
import { createDeadlineFetch } from './network'

// Use actual loopback sockets rather than mocking fetch or the HTTP response.
let server: Server | undefined
async function listen(): Promise<string> {
  server = createServer((req, res) => {
    if (req.url === '/ok') res.end('real response')
    // /stall intentionally sends no response headers.
  })
  await new Promise<void>((resolve, reject) => {
    server!.once('error', reject)
    server!.listen(0, '127.0.0.1', resolve)
  })
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('Missing test server address')
  return `http://127.0.0.1:${address.port}`
}
afterEach(async () => {
  if (!server) return
  const current = server
  server = undefined
  current.closeAllConnections()
  await new Promise<void>(resolve => current.close(() => resolve()))
})

describe('HTTP deadlines over real sockets', () => {
  it('returns a real HTTP response', async () => {
    const base = await listen()
    const response = await createDeadlineFetch(2000)(base + '/ok')
    expect(await response.text()).toBe('real response')
  })
  it('aborts a connection that never returns headers', async () => {
    const base = await listen()
    await expect(createDeadlineFetch(150)(base + '/stall')).rejects.toMatchObject({ name: 'TimeoutError' })
  })
  it('preserves the caller cancellation signal', async () => {
    const base = await listen()
    const controller = new AbortController()
    const pending = createDeadlineFetch(2000)(base + '/stall', { signal: controller.signal })
    controller.abort(new DOMException('Cancelled by caller', 'AbortError'))
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
  })
  it('rejects invalid timeout configuration', () => {
    expect(() => createDeadlineFetch(0)).toThrow('Invalid network timeout')
    expect(() => createDeadlineFetch(Number.NaN)).toThrow('Invalid network timeout')
  })
})
