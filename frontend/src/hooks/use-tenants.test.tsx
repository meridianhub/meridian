import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

// Mock useApiClients so we don't need a real transport
vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

import { useApiClients } from '@/api/context'
import { useTenants } from './use-tenants'

const mockTenants = [
  {
    tenantId: 'acme_corp',
    slug: 'acme-bank',
    displayName: 'Acme Bank',
    status: 1,
    settlementAsset: 'GBP',
    subdomain: '',
    partyId: '',
    errorMessage: '',
    version: 1,
    createdAt: undefined,
    deprovisionedAt: undefined,
    metadata: undefined,
  },
  {
    tenantId: 'beta_corp',
    slug: 'beta-financial',
    displayName: 'Beta Financial',
    status: 1,
    settlementAsset: 'USD',
    subdomain: '',
    partyId: '',
    errorMessage: '',
    version: 1,
    createdAt: undefined,
    deprovisionedAt: undefined,
    metadata: undefined,
  },
]

function makeWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

describe('useTenants', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('returns tenants from API on success', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        listTenants: vi.fn().mockResolvedValue({ tenants: mockTenants, nextPageToken: '' }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useTenants(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(result.current.data).toEqual(mockTenants)
  })

  it('returns error state when API fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        listTenants: vi.fn().mockRejectedValue(new Error('Network error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useTenants(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(result.current.error).toBeInstanceOf(Error)
  })

  it('returns loading state initially', () => {
    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        listTenants: vi.fn().mockReturnValue(new Promise(() => {})),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useTenants(), { wrapper: makeWrapper() })

    expect(result.current.isLoading).toBe(true)
  })

  it('uses platform query key for caching', async () => {
    const listTenants = vi.fn().mockResolvedValue({ tenants: mockTenants, nextPageToken: '' })
    vi.mocked(useApiClients).mockReturnValue({
      tenant: { listTenants },
    } as unknown as ReturnType<typeof useApiClients>)

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useTenants(), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    // Should be cached under platform.tenants key
    const cached = queryClient.getQueryData(['platform', 'tenants'])
    expect(cached).toEqual(mockTenants)
  })

  it('returns empty array when API returns no tenants', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        listTenants: vi.fn().mockResolvedValue({ tenants: [], nextPageToken: '' }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useTenants(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(result.current.data).toEqual([])
  })
})
