import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { useManifestDiff } from './use-manifest-diff'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

import { useApiClients } from '@/api/context'

function createWrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
}

const mockDiffResponse = {
  actions: [],
  summary: { totalActions: 2, creates: 1, updates: 1, deletes: 0, noChanges: 0, hasBreakingChanges: false },
}

beforeEach(() => {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      diffManifestVersions: vi.fn().mockResolvedValue(mockDiffResponse),
    },
  } as unknown as ReturnType<typeof useApiClients>)
})

describe('useManifestDiff', () => {
  it('returns loading state initially', () => {
    const { result } = renderHook(() => useManifestDiff(1, 2), { wrapper: createWrapper() })
    expect(result.current.isLoading).toBe(true)
    expect(result.current.data).toBeUndefined()
  })

  it('returns data after successful fetch', async () => {
    const { result } = renderHook(() => useManifestDiff(1, 2), { wrapper: createWrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.data).toEqual(mockDiffResponse)
    expect(result.current.error).toBeNull()
  })

  it('does not fetch when targetSeq is 0', () => {
    const diffFn = vi.fn()
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: { diffManifestVersions: diffFn },
    } as unknown as ReturnType<typeof useApiClients>)

    renderHook(() => useManifestDiff(0, 0), { wrapper: createWrapper() })
    expect(diffFn).not.toHaveBeenCalled()
  })

  it('returns error when fetch fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        diffManifestVersions: vi.fn().mockRejectedValue(new Error('API error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useManifestDiff(1, 2), { wrapper: createWrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.data).toBeUndefined()
  })

  it('calls API with correct BigInt sequence numbers', async () => {
    const diffFn = vi.fn().mockResolvedValue(mockDiffResponse)
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: { diffManifestVersions: diffFn },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useManifestDiff(3, 7), { wrapper: createWrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(diffFn).toHaveBeenCalledWith({
      baseSequenceNumber: BigInt(3),
      targetSequenceNumber: BigInt(7),
    })
  })
})
