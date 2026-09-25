/** Bound connection establishment; the MCP request deadline also bounds its body. */
export function createDeadlineFetch(timeoutMs = 8_000): typeof fetch {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) throw new Error('Invalid network timeout')
  return async (input, init = {}) => {
    const controller = new AbortController()
    const upstream = init.signal ?? (input instanceof Request ? input.signal : undefined)
    const relayAbort = () => controller.abort(upstream?.reason)
    if (upstream?.aborted) relayAbort()
    else upstream?.addEventListener('abort', relayAbort, { once: true })
    const timer = setTimeout(() => controller.abort(new DOMException('Ledger request timed out', 'TimeoutError')), timeoutMs)
    try {
      return await fetch(input, { ...init, signal: controller.signal })
    } finally {
      clearTimeout(timer)
      upstream?.removeEventListener('abort', relayAbort)
    }
  }
}
