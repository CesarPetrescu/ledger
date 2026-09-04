import { describe, expect, it, vi } from 'vitest'
import { startLiveUpdates, subscribeLiveChanges } from '../live'

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this)
  }

  close() {
    this.onclose?.({} as CloseEvent)
  }
}

describe('live updates', () => {
  it('opens one admin event stream and forwards committed changes', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket)
    const changed = vi.fn()
    const unsubscribe = subscribeLiveChanges(changed)
    const stop = startLiveUpdates()
    const socket = FakeWebSocket.instances.at(-1)!

    expect(socket.url).toBe('ws://localhost:3000/admin/api/events')
    socket.onmessage?.({ data: '{"type":"heartbeat"}' } as MessageEvent)
    socket.onmessage?.({ data: '{"type":"change","entity":"entry"}' } as MessageEvent)
    expect(changed).toHaveBeenCalledWith('entry')

    unsubscribe()
    stop()
  })
})
