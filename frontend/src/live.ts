import { useEffect, useSyncExternalStore } from 'react'

export type LiveStatus = 'connecting' | 'live' | 'offline'

let status: LiveStatus = 'connecting'
let socket: WebSocket | null = null
let retry: number | null = null
let generation = 0
let users = 0
const statusListeners = new Set<() => void>()
const changeListeners = new Set<(entity: string) => void>()

function setStatus(next: LiveStatus) {
  if (status === next) return
  status = next
  for (const listener of statusListeners) listener()
}

function connect(currentGeneration: number) {
  if (users === 0 || currentGeneration !== generation || socket || typeof WebSocket === 'undefined') {
    if (typeof WebSocket === 'undefined') setStatus('offline')
    return
  }
  setStatus('connecting')
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let next: WebSocket
  try {
    next = new WebSocket(`${scheme}//${window.location.host}/admin/api/events`)
  } catch {
    setStatus('offline')
    retry = window.setTimeout(() => connect(currentGeneration), 1500)
    return
  }
  socket = next
  next.onopen = () => {
    if (currentGeneration === generation) setStatus('live')
  }
  next.onmessage = (event) => {
    if (typeof event.data !== 'string' || event.data.includes('"heartbeat"')) return
    try {
      const message = JSON.parse(event.data) as { type?: string; entity?: string }
      if (message.type === 'change' && message.entity) {
        for (const listener of changeListeners) listener(message.entity)
      }
    } catch {
      // Ignore malformed server events and keep the stream alive.
    }
  }
  next.onclose = () => {
    if (socket === next) socket = null
    if (users === 0 || currentGeneration !== generation) return
    setStatus('offline')
    retry = window.setTimeout(() => connect(currentGeneration), 1500)
  }
  next.onerror = () => next.close()
}

export function startLiveUpdates(): () => void {
  users += 1
  if (users === 1) {
    generation += 1
    connect(generation)
  }
  return () => {
    users = Math.max(0, users - 1)
    if (users !== 0) return
    generation += 1
    if (retry !== null) window.clearTimeout(retry)
    retry = null
    const current = socket
    socket = null
    current?.close(1000, 'signed out')
    setStatus('offline')
  }
}

export function subscribeLiveChanges(listener: (entity: string) => void): () => void {
  changeListeners.add(listener)
  return () => changeListeners.delete(listener)
}

function subscribeStatus(listener: () => void): () => void {
  statusListeners.add(listener)
  return () => statusListeners.delete(listener)
}

export function useLiveUpdates(): LiveStatus {
  useEffect(startLiveUpdates, [])
  return useSyncExternalStore(subscribeStatus, () => status, () => 'offline')
}
