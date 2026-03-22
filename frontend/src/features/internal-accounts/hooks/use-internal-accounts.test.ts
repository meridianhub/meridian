import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as React from 'react'

const mockListInternalAccounts = vi.fn()
const mockRetrieveInternalAccount = vi.fn()

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    internalAccount: {
      listInternalAccounts: mockListInternalAccounts,
      retrieveInternalAccount: mockRetrieveInternalAccount,
    },
  })),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: vi.fn(() => 'test-tenant'),
}))

import { useInternalAccountsTable, useInternalAccountDetail } from './use-internal-accounts'
import { useTenantSlug } from '@/hooks/use-tenant-context'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
    logger: { log: () => {}, warn: () => {}, error: () => {} },
  })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client: queryClient }, children)
  }
}

describe('useInternalAccountsTable', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useTenantSlug).mockReturnValue('test-tenant')
  })

  it('returns queryKey, queryFn, and tenantSlug', () => {
    const { result } = renderHook(() => useInternalAccountsTable(), {
      wrapper: createWrapper(),
    })
    expect(result.current.queryKey).toBeDefined()
    expect(result.current.queryFn).toBeTypeOf('function')
    expect(result.current.tenantSlug).toBe('test-tenant')
  })

  it('queryKey includes tenantSlug', () => {
    const { result } = renderHook(() => useInternalAccountsTable(), {
      wrapper: createWrapper(),
    })
    expect(result.current.queryKey).toEqual(
      expect.arrayContaining(['test-tenant']),
    )
  })

  it('queryFn returns empty items when tenantSlug is null', async () => {
    vi.mocked(useTenantSlug).mockReturnValue(null)
    const { result } = renderHook(() => useInternalAccountsTable(), {
      wrapper: createWrapper(),
    })
    const data = await result.current.queryFn({ pageSize: 10 })
    expect(data).toEqual({ items: [] })
  })

  it('queryFn maps facilities to InternalAccountRow shape', async () => {
    mockListInternalAccounts.mockResolvedValue({
      facilities: [
        {
          accountId: 'acc-001',
          accountCode: 'CLR-GBP-001',
          name: 'GBP Clearing',
          behaviorClass: 'CLEARING',
          accountStatus: 1,
          instrumentCode: 'GBP',
          createdAt: { seconds: BigInt(1700000000), nanos: 0 },
        },
      ],
      pagination: { nextPageToken: 'tok-next', totalCount: BigInt(1) },
    })

    const { result } = renderHook(() => useInternalAccountsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items).toHaveLength(1)
    expect(data.items[0]).toMatchObject({
      accountId: 'acc-001',
      accountCode: 'CLR-GBP-001',
      name: 'GBP Clearing',
      behaviorClass: 'CLEARING',
      accountStatus: 1,
      instrumentCode: 'GBP',
    })
    expect(data.nextPageToken).toBe('tok-next')
  })

  it('queryFn passes statusFilter parsed from filters', async () => {
    mockListInternalAccounts.mockResolvedValue({
      facilities: [],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useInternalAccountsTable(), {
      wrapper: createWrapper(),
    })

    await result.current.queryFn({ pageSize: 10, filters: { status: '2' } })

    expect(mockListInternalAccounts).toHaveBeenCalledWith(
      expect.objectContaining({ statusFilter: 2 }),
    )
  })

  it('queryFn passes behaviorClassFilter from filters', async () => {
    mockListInternalAccounts.mockResolvedValue({
      facilities: [],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useInternalAccountsTable(), {
      wrapper: createWrapper(),
    })

    await result.current.queryFn({ pageSize: 10, filters: { behaviorClass: 'CLEARING' } })

    expect(mockListInternalAccounts).toHaveBeenCalledWith(
      expect.objectContaining({ behaviorClassFilter: 'CLEARING' }),
    )
  })

  it('queryFn returns undefined nextPageToken when pagination is empty', async () => {
    mockListInternalAccounts.mockResolvedValue({
      facilities: [],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useInternalAccountsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })
    expect(data.nextPageToken).toBeUndefined()
  })
})

describe('useInternalAccountDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useTenantSlug).mockReturnValue('test-tenant')
  })

  it('returns account data on success', async () => {
    mockRetrieveInternalAccount.mockResolvedValue({
      facility: {
        accountId: 'acc-001',
        accountCode: 'CLR-GBP-001',
        name: 'GBP Clearing',
        behaviorClass: 'CLEARING',
        instrumentCode: 'GBP',
        accountStatus: 1,
        description: 'Test account',
        createdAt: { seconds: BigInt(1700000000), nanos: 0 },
        updatedAt: { seconds: BigInt(1700000001), nanos: 0 },
      },
    })

    const { result } = renderHook(() => useInternalAccountDetail('acc-001'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(result.current.data).toMatchObject({
      accountId: 'acc-001',
      accountCode: 'CLR-GBP-001',
      name: 'GBP Clearing',
      behaviorClass: 'CLEARING',
      instrumentCode: 'GBP',
      accountStatus: 1,
      description: 'Test account',
    })
  })

  it('returns null when facility is missing in response', async () => {
    mockRetrieveInternalAccount.mockResolvedValue({ facility: null })

    const { result } = renderHook(() => useInternalAccountDetail('acc-001'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toBeNull()
  })

  it('returns null for NotFound error', async () => {
    const { ConnectError, Code } = await import('@connectrpc/connect')
    mockRetrieveInternalAccount.mockRejectedValue(
      new ConnectError('not found', Code.NotFound),
    )

    const { result } = renderHook(() => useInternalAccountDetail('acc-missing'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toBeNull()
  })

  it('propagates non-NotFound errors', async () => {
    const { ConnectError, Code } = await import('@connectrpc/connect')
    mockRetrieveInternalAccount.mockRejectedValue(
      new ConnectError('internal error', Code.Internal),
    )

    const { result } = renderHook(() => useInternalAccountDetail('acc-001'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeDefined()
  })

  it('is disabled when accountId is undefined', () => {
    const { result } = renderHook(() => useInternalAccountDetail(undefined), {
      wrapper: createWrapper(),
    })

    expect(result.current.isFetching).toBe(false)
    expect(mockRetrieveInternalAccount).not.toHaveBeenCalled()
  })

  it('is disabled when tenantSlug is null', () => {
    vi.mocked(useTenantSlug).mockReturnValue(null)

    const { result } = renderHook(() => useInternalAccountDetail('acc-001'), {
      wrapper: createWrapper(),
    })

    expect(result.current.isFetching).toBe(false)
    expect(mockRetrieveInternalAccount).not.toHaveBeenCalled()
  })
})
