import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { usePatternFiles } from './use-pattern-files'

describe('usePatternFiles', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('returns null content when no pattern name provided', () => {
    const { result } = renderHook(() => usePatternFiles(undefined))
    expect(result.current.starlarkContent).toBeNull()
    expect(result.current.manifestContent).toBeNull()
    expect(result.current.isLoading).toBe(false)
  })

  it('fetches starlark and manifest files', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
      const path = typeof url === 'string' ? url : url.toString()
      if (path.includes('saga.star')) {
        return new Response('def execute():\n  pass', { status: 200 })
      }
      if (path.includes('manifest.yaml')) {
        return new Response('name: test\ntype: registry:pattern', { status: 200 })
      }
      return new Response('', { status: 404 })
    })

    const { result } = renderHook(() => usePatternFiles('test-pattern'))

    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(result.current.starlarkContent).toBe('def execute():\n  pass')
    expect(result.current.manifestContent).toBe('name: test\ntype: registry:pattern')
  })

  it('returns null for files that return 404', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('', { status: 404 }))

    const { result } = renderHook(() => usePatternFiles('missing-pattern'))

    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(result.current.starlarkContent).toBeNull()
    expect(result.current.manifestContent).toBeNull()
  })

  it('returns null for fetch errors', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('Network error'))

    const { result } = renderHook(() => usePatternFiles('broken-pattern'))

    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(result.current.starlarkContent).toBeNull()
    expect(result.current.manifestContent).toBeNull()
  })
})
