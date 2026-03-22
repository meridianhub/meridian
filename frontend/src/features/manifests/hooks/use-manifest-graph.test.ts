import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { useManifestGraph } from './use-manifest-graph'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

import { useApiClients } from '@/api/context'

function createWrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
}

const mockManifest = {
  version: '1.0',
  metadata: { name: 'Test', industry: 'energy', description: '' },
  instruments: [
    { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
  ],
  accountTypes: [],
  valuationRules: [],
  sagas: [],
  seedData: undefined,
  paymentRails: [],
  partyTypes: [],
  mappings: [],
  operationalGateway: undefined,
}

beforeEach(() => {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({
        version: { manifest: mockManifest },
      }),
    },
  } as unknown as ReturnType<typeof useApiClients>)
})

describe('useManifestGraph', () => {
  it('returns loading state initially', () => {
    const { result } = renderHook(() => useManifestGraph(), { wrapper: createWrapper() })
    expect(result.current.isLoading).toBe(true)
    expect(result.current.graph).toBeNull()
  })

  it('returns graph after successful fetch', async () => {
    const { result } = renderHook(() => useManifestGraph(), { wrapper: createWrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.graph).not.toBeNull()
    expect(result.current.error).toBeNull()
  })

  it('returns graph with nodes derived from manifest', async () => {
    const { result } = renderHook(() => useManifestGraph(), { wrapper: createWrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    const graph = result.current.graph!
    expect(graph.nodes.some((n) => n.id === 'instrument:GBP')).toBe(true)
  })

  it('returns null graph when manifest is absent', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockResolvedValue({ version: undefined }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useManifestGraph(), { wrapper: createWrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.graph).toBeNull()
  })

  it('returns error when fetch fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockRejectedValue(new Error('Fetch failed')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useManifestGraph(), { wrapper: createWrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.graph).toBeNull()
  })
})
