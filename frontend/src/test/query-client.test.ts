import { describe, it, expect } from 'vitest'
import { queryClient } from '@/lib/query-client'
import { QueryClient } from '@tanstack/react-query'

describe('queryClient', () => {
  it('is a QueryClient instance', () => {
    expect(queryClient).toBeInstanceOf(QueryClient)
  })

  it('has correct staleTime default', () => {
    const options = queryClient.getDefaultOptions()
    expect(options.queries?.staleTime).toBe(30_000)
  })

  it('has correct gcTime default', () => {
    const options = queryClient.getDefaultOptions()
    expect(options.queries?.gcTime).toBe(5 * 60 * 1000)
  })

  it('has correct retry count for queries', () => {
    const options = queryClient.getDefaultOptions()
    expect(options.queries?.retry).toBe(2)
  })

  it('has no retry for mutations', () => {
    const options = queryClient.getDefaultOptions()
    expect(options.mutations?.retry).toBe(0)
  })

  it('has refetchOnWindowFocus enabled', () => {
    const options = queryClient.getDefaultOptions()
    expect(options.queries?.refetchOnWindowFocus).toBe(true)
  })

  it('has refetchOnReconnect enabled', () => {
    const options = queryClient.getDefaultOptions()
    expect(options.queries?.refetchOnReconnect).toBe(true)
  })

  it('retryDelay uses exponential backoff capped at 10s', () => {
    const options = queryClient.getDefaultOptions()
    const retryDelay = options.queries?.retryDelay as (attempt: number) => number
    expect(retryDelay(0)).toBe(1000)
    expect(retryDelay(1)).toBe(2000)
    expect(retryDelay(2)).toBe(4000)
    expect(retryDelay(10)).toBe(10_000)
  })
})
