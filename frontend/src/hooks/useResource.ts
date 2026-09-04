import { useCallback, useEffect, useEffectEvent, useState } from 'react'
import { describeError } from '../api'

interface Settled<T> {
  request: string
  data?: T | undefined
  error?: string | undefined
}

export interface Resource<T> {
  data: T | undefined
  error: string | undefined
  loading: boolean
  refreshing: boolean
  stale: boolean
  reload: () => void
  update: (fn: (previous: T) => T) => void
}

/**
 * Loads `key`-addressed data, keeps the last good value while refreshing, and
 * exposes stale/error/retry state. Responses for superseded requests are ignored.
 */
export function useResource<T>(load: () => Promise<T>, key = ''): Resource<T> {
  const [attempt, setAttempt] = useState(0)
  const [settled, setSettled] = useState<Settled<T>>({ request: '' })
  const request = `${key}#${attempt}`
  const run = useEffectEvent(() => load())

  useEffect(() => {
    let active = true
    run().then(
      (data) => {
        if (active) setSettled({ request, data })
      },
      (error: unknown) => {
        if (active)
          setSettled((previous) => ({
            request,
            data: previous.request.slice(0, previous.request.lastIndexOf('#')) === key ? previous.data : undefined,
            error: describeError(error),
          }))
      },
    )
    return () => {
      active = false
    }
  }, [request])

  const current = settled.request === request
  const sameKey = settled.request.slice(0, settled.request.lastIndexOf('#')) === key
  const data = sameKey ? settled.data : undefined
  const reload = useCallback(() => setAttempt((value) => value + 1), [])
  const update = useCallback((fn: (previous: T) => T) => {
    setSettled((previous) => (previous.data === undefined ? previous : { ...previous, data: fn(previous.data) }))
  }, [])

  return {
    data,
    error: current ? settled.error : undefined,
    loading: !current && data === undefined,
    refreshing: !current && data !== undefined,
    stale: current && settled.error !== undefined && data !== undefined,
    reload,
    update,
  }
}
