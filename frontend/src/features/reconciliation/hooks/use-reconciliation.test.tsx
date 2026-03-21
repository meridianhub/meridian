import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import type { ReactNode } from 'react'
import { useReconciliationRunsTable, useReconciliationRunDetail } from './use-reconciliation'

const mockListReconciliationRuns = vi.fn()
const mockGetReconciliationRun = vi.fn()

vi.mock('@/api/context', () => ({
  useApiClients: () => ({
    accountReconciliation: {
      listReconciliationRuns: mockListReconciliationRuns,
      getReconciliationRun: mockGetReconciliationRun,
    },
  }),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
}))

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

describe('useReconciliationRunsTable', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('returns queryKey and queryFn', () => {
    const { result } = renderHook(() => useReconciliationRunsTable(), {
      wrapper: createWrapper(),
    })

    expect(result.current.queryKey).toEqual(['tenants', 'test-tenant', 'reconciliation-runs'])
    expect(typeof result.current.queryFn).toBe('function')
    expect(result.current.tenantSlug).toBe('test-tenant')
  })

  it('queryFn maps response and strips prefixes', async () => {
    mockListReconciliationRuns.mockResolvedValue({
      runs: [
        {
          runId: 'run-1',
          accountId: 'acc-1',
          scope: 'RECONCILIATION_SCOPE_DAILY',
          settlementType: 'SETTLEMENT_TYPE_NET',
          status: 'RUN_STATUS_COMPLETED',
          varianceCount: 3,
          periodStart: '2025-01-01',
          periodEnd: '2025-01-31',
        },
      ],
      nextPageToken: 'next-tok',
    })

    const { result } = renderHook(() => useReconciliationRunsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items).toHaveLength(1)
    expect(data.items[0]).toEqual({
      runId: 'run-1',
      accountId: 'acc-1',
      scope: 'DAILY',
      settlementType: 'NET',
      status: 'COMPLETED',
      varianceCount: 3,
      periodStart: '2025-01-01',
      periodEnd: '2025-01-31',
    })
    expect(data.nextPageToken).toBe('next-tok')
  })

  it('queryFn passes filters to API', async () => {
    mockListReconciliationRuns.mockResolvedValue({ runs: [], nextPageToken: '' })

    const { result } = renderHook(() => useReconciliationRunsTable(), {
      wrapper: createWrapper(),
    })

    await result.current.queryFn({
      pageSize: 20,
      pageToken: 'tok',
      filters: { status: 'COMPLETED', account_id: 'acc-1' },
    })

    expect(mockListReconciliationRuns).toHaveBeenCalledWith({
      pageSize: 20,
      pageToken: 'tok',
      status: 'COMPLETED',
      accountId: 'acc-1',
    })
  })

  it('queryFn handles null fields gracefully', async () => {
    mockListReconciliationRuns.mockResolvedValue({
      runs: [
        {
          runId: null,
          accountId: null,
          scope: null,
          settlementType: null,
          status: null,
          varianceCount: null,
          periodStart: null,
          periodEnd: null,
        },
      ],
      nextPageToken: '',
    })

    const { result } = renderHook(() => useReconciliationRunsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items[0]).toEqual({
      runId: '',
      accountId: '',
      scope: '',
      settlementType: '',
      status: '',
      varianceCount: 0,
      periodStart: '',
      periodEnd: '',
    })
  })

  it('queryFn returns empty on NotFound error', async () => {
    mockListReconciliationRuns.mockRejectedValue(new ConnectError('not found', Code.NotFound))

    const { result } = renderHook(() => useReconciliationRunsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })
    expect(data).toEqual({ items: [] })
  })

  it('queryFn returns empty on Unimplemented error', async () => {
    mockListReconciliationRuns.mockRejectedValue(
      new ConnectError('unimplemented', Code.Unimplemented),
    )

    const { result } = renderHook(() => useReconciliationRunsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })
    expect(data).toEqual({ items: [] })
  })

  it('queryFn rethrows other errors', async () => {
    mockListReconciliationRuns.mockRejectedValue(new Error('Server error'))

    const { result } = renderHook(() => useReconciliationRunsTable(), {
      wrapper: createWrapper(),
    })

    await expect(result.current.queryFn({ pageSize: 10 })).rejects.toThrow('Server error')
  })
})

describe('useReconciliationRunDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('fetches and maps run detail', async () => {
    mockGetReconciliationRun.mockResolvedValue({
      runId: 'run-1',
      accountId: 'acc-1',
      scope: 'RECONCILIATION_SCOPE_MONTHLY',
      settlementType: 'SETTLEMENT_TYPE_GROSS',
      status: 'RUN_STATUS_FAILED',
      varianceCount: 5,
      periodStart: '2025-01-01',
      periodEnd: '2025-01-31',
    })

    const { result } = renderHook(() => useReconciliationRunDetail('run-1'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => {
      expect(result.current.data).toBeDefined()
    })

    expect(result.current.data).toEqual({
      runId: 'run-1',
      accountId: 'acc-1',
      scope: 'MONTHLY',
      settlementType: 'GROSS',
      status: 'FAILED',
      varianceCount: 5,
      periodStart: '2025-01-01',
      periodEnd: '2025-01-31',
    })
  })

  it('does not fetch when runId is undefined', () => {
    renderHook(() => useReconciliationRunDetail(undefined), {
      wrapper: createWrapper(),
    })

    expect(mockGetReconciliationRun).not.toHaveBeenCalled()
  })
})
