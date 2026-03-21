import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import type { ReactNode } from 'react'
import { usePaymentsTable, usePaymentDetail } from './use-payments'

const mockListPaymentOrders = vi.fn()
const mockRetrievePaymentOrder = vi.fn()

vi.mock('@/api/context', () => ({
  useApiClients: () => ({
    paymentOrder: {
      listPaymentOrders: mockListPaymentOrders,
      retrievePaymentOrder: mockRetrievePaymentOrder,
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

describe('usePaymentsTable', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('returns queryKey and queryFn', () => {
    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    expect(result.current.queryKey).toEqual(['tenants', 'test-tenant', 'payments'])
    expect(typeof result.current.queryFn).toBe('function')
    expect(result.current.tenantSlug).toBe('test-tenant')
  })

  it('queryFn maps response correctly', async () => {
    mockListPaymentOrders.mockResolvedValue({
      paymentOrders: [
        {
          paymentOrderId: 'po-1',
          debtorAccountId: 'acc-1',
          creditorReference: 'CR001',
          amount: '100.00',
          currency: 'GBP',
          status: 'COMPLETED',
          createdAt: '2025-01-15T10:00:00Z',
        },
      ],
      pagination: { nextPageToken: 'next-page' },
    })

    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items).toHaveLength(1)
    expect(data.items[0]).toEqual({
      paymentOrderId: 'po-1',
      debtorAccountId: 'acc-1',
      creditorReference: 'CR001',
      amount: '100.00',
      currency: 'GBP',
      status: 'COMPLETED',
      createdAt: '2025-01-15T10:00:00Z',
    })
    expect(data.nextPageToken).toBe('next-page')
  })

  it('queryFn passes pagination and filters', async () => {
    mockListPaymentOrders.mockResolvedValue({
      paymentOrders: [],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    await result.current.queryFn({
      pageSize: 25,
      pageToken: 'tok',
      filters: { status: 'PENDING' },
    })

    expect(mockListPaymentOrders).toHaveBeenCalledWith({
      pagination: { pageSize: 25, pageToken: 'tok' },
      status: 'PENDING',
    })
  })

  it('queryFn handles null fields gracefully', async () => {
    mockListPaymentOrders.mockResolvedValue({
      paymentOrders: [
        {
          paymentOrderId: null,
          debtorAccountId: null,
          creditorReference: null,
          amount: null,
          currency: null,
          status: null,
          createdAt: null,
        },
      ],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items[0]).toEqual({
      paymentOrderId: '',
      debtorAccountId: '',
      creditorReference: '',
      amount: '',
      currency: '',
      status: '',
      createdAt: null,
    })
  })

  it('queryFn returns empty on NotFound error', async () => {
    mockListPaymentOrders.mockRejectedValue(new ConnectError('not found', Code.NotFound))

    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })
    expect(data).toEqual({ items: [] })
  })

  it('queryFn returns empty on Unimplemented error', async () => {
    mockListPaymentOrders.mockRejectedValue(
      new ConnectError('unimplemented', Code.Unimplemented),
    )

    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })
    expect(data).toEqual({ items: [] })
  })

  it('queryFn rethrows other errors', async () => {
    mockListPaymentOrders.mockRejectedValue(new Error('Internal error'))

    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    await expect(result.current.queryFn({ pageSize: 10 })).rejects.toThrow('Internal error')
  })

  it('queryFn returns empty when no nextPageToken in pagination', async () => {
    mockListPaymentOrders.mockResolvedValue({
      paymentOrders: [],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => usePaymentsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })
    expect(data.nextPageToken).toBeUndefined()
  })
})

describe('usePaymentDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('fetches and maps payment detail', async () => {
    mockRetrievePaymentOrder.mockResolvedValue({
      paymentOrder: {
        paymentOrderId: 'po-1',
        debtorAccountId: 'acc-1',
        creditorReference: 'CR001',
        amount: '250.00',
        currency: 'GBP',
        status: 'COMPLETED',
        reference: 'REF-123',
        createdAt: { seconds: BigInt(1700000000), nanos: 0 },
        sagaSteps: [{ name: 'step1', status: 'SUCCESS' }],
        compensationSteps: [],
      },
    })

    const { result } = renderHook(() => usePaymentDetail('po-1'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => {
      expect(result.current.data).toBeDefined()
    })

    expect(result.current.data).toEqual({
      paymentOrderId: 'po-1',
      debtorAccountId: 'acc-1',
      creditorReference: 'CR001',
      amount: '250.00',
      currency: 'GBP',
      status: 'COMPLETED',
      reference: 'REF-123',
      createdAt: { seconds: BigInt(1700000000), nanos: 0 },
      sagaSteps: [{ name: 'step1', status: 'SUCCESS' }],
      compensationSteps: [],
    })
  })

  it('returns null when paymentOrder is missing from response', async () => {
    mockRetrievePaymentOrder.mockResolvedValue({
      paymentOrder: undefined,
    })

    const { result } = renderHook(() => usePaymentDetail('po-1'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true)
    })

    expect(result.current.data).toBeNull()
  })

  it('does not fetch when paymentOrderId is undefined', () => {
    renderHook(() => usePaymentDetail(undefined), {
      wrapper: createWrapper(),
    })

    expect(mockRetrievePaymentOrder).not.toHaveBeenCalled()
  })
})
