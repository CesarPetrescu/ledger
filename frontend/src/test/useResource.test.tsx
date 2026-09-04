import { renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useResource } from '../hooks/useResource'

describe('useResource', () => {
  it('does not carry stale data across resource keys when the next request fails', async () => {
    const { result, rerender } = renderHook(
      ({ resourceKey, load }: { resourceKey: string; load: () => Promise<string> }) => useResource(load, resourceKey),
      { initialProps: { resourceKey: 'page:0', load: async () => 'first page' } },
    )

    await waitFor(() => expect(result.current.data).toBe('first page'))

    rerender({
      resourceKey: 'page:50',
      load: async () => {
        throw new Error('next page failed')
      },
    })

    await waitFor(() => expect(result.current.error).toBe('Something went wrong.'))
    expect(result.current.data).toBeUndefined()
    expect(result.current.stale).toBe(false)
  })
})