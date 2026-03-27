import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import type { ReactNode } from 'react'
import {
  useBillingRunsTable,
  useInvoicesTable,
  useInvoiceDetail,
  useInvoiceEmails,
  useResendInvoiceEmail,
  useMarkInvoicePaid,
  useVoidInvoice,
} from './hooks'

const mockListBillingRuns = vi.fn()
const mockListInvoices = vi.fn()
const mockGetInvoice = vi.fn()
const mockListInvoiceEmails = vi.fn()
const mockResendInvoiceEmail = vi.fn()
const mockMarkInvoicePaid = vi.fn()
const mockVoidInvoice = vi.fn()

vi.mock('@/api/context', () => ({
  useApiClients: () => ({
    billing: {
      listBillingRuns: mockListBillingRuns,
      listInvoices: mockListInvoices,
      getInvoice: mockGetInvoice,
      listInvoiceEmails: mockListInvoiceEmails,
      resendInvoiceEmail: mockResendInvoiceEmail,
      markInvoicePaid: mockMarkInvoicePaid,
      voidInvoice: mockVoidInvoice,
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

describe('useBillingRunsTable', () => {
  beforeEach(() => vi.clearAllMocks())

  it('returns queryKey and queryFn', () => {
    const { result } = renderHook(() => useBillingRunsTable(), {
      wrapper: createWrapper(),
    })
    expect(result.current.queryKey).toEqual(['tenants', 'test-tenant', 'billing-runs'])
    expect(typeof result.current.queryFn).toBe('function')
    expect(result.current.tenantSlug).toBe('test-tenant')
  })

  it('queryFn maps response correctly', async () => {
    mockListBillingRuns.mockResolvedValue({
      billingRuns: [
        {
          id: 'run-1',
          periodStart: { seconds: BigInt(1700000000), nanos: 0 },
          periodEnd: { seconds: BigInt(1700086400), nanos: 0 },
          status: 'BILLING_RUN_STATUS_COMPLETED',
          dunningLevel: 0,
          invoiceCount: 5,
          totalAmountCents: BigInt(50000),
          currency: 'GBP',
          createdAt: { seconds: BigInt(1700000000), nanos: 0 },
        },
      ],
      pagination: { nextPageToken: 'next-page' },
    })

    const { result } = renderHook(() => useBillingRunsTable(), {
      wrapper: createWrapper(),
    })

    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items).toHaveLength(1)
    expect(data.items[0].id).toBe('run-1')
    expect(data.items[0].status).toBe('COMPLETED')
    expect(data.items[0].invoiceCount).toBe(5)
    expect(data.items[0].totalAmountCents).toBe(50000)
    expect(data.items[0].currency).toBe('GBP')
    expect(data.nextPageToken).toBe('next-page')
  })

  it('queryFn strips BILLING_RUN_STATUS_ prefix', async () => {
    mockListBillingRuns.mockResolvedValue({
      billingRuns: [
        { id: 'r1', status: 'BILLING_RUN_STATUS_INITIATED', totalAmountCents: BigInt(0) },
      ],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useBillingRunsTable(), { wrapper: createWrapper() })
    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items[0].status).toBe('INITIATED')
  })

  it('queryFn returns empty on NotFound', async () => {
    mockListBillingRuns.mockRejectedValue(new ConnectError('not found', Code.NotFound))

    const { result } = renderHook(() => useBillingRunsTable(), { wrapper: createWrapper() })
    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data).toEqual({ items: [] })
  })

  it('queryFn returns empty on Unimplemented', async () => {
    mockListBillingRuns.mockRejectedValue(new ConnectError('unimplemented', Code.Unimplemented))

    const { result } = renderHook(() => useBillingRunsTable(), { wrapper: createWrapper() })
    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data).toEqual({ items: [] })
  })

  it('queryFn rethrows other errors', async () => {
    mockListBillingRuns.mockRejectedValue(new Error('server error'))

    const { result } = renderHook(() => useBillingRunsTable(), { wrapper: createWrapper() })
    await expect(result.current.queryFn({ pageSize: 10 })).rejects.toThrow('server error')
  })
})

describe('useInvoicesTable', () => {
  beforeEach(() => vi.clearAllMocks())

  it('returns queryKey and queryFn', () => {
    const { result } = renderHook(() => useInvoicesTable(), { wrapper: createWrapper() })
    expect(result.current.queryKey).toEqual(['tenants', 'test-tenant', 'invoices'])
    expect(typeof result.current.queryFn).toBe('function')
  })

  it('queryFn maps invoice response correctly', async () => {
    mockListInvoices.mockResolvedValue({
      invoices: [
        {
          id: 'inv-1',
          billingRunId: 'run-1',
          partyId: 'party-1',
          invoiceNumber: 'INV-2026-00001',
          lineItems: [
            {
              description: 'Monthly fee',
              quantity: '1',
              unitPriceCents: BigInt(1000),
              totalCents: BigInt(1000),
              valuationAnalysis: null,
            },
          ],
          subtotalCents: BigInt(1000),
          currency: 'GBP',
          status: 'INVOICE_STATUS_ISSUED',
          dueDate: '2026-04-01',
          createdAt: { seconds: BigInt(1700000000), nanos: 0 },
          updatedAt: { seconds: BigInt(1700000000), nanos: 0 },
        },
      ],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useInvoicesTable(), { wrapper: createWrapper() })
    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items).toHaveLength(1)
    expect(data.items[0].id).toBe('inv-1')
    expect(data.items[0].status).toBe('ISSUED')
    expect(data.items[0].subtotalCents).toBe(1000)
    expect(data.items[0].lineItems[0].quantity).toBe('1')
    expect(data.items[0].dueDate).toBe('2026-04-01')
  })

  it('queryFn strips INVOICE_STATUS_ prefix', async () => {
    mockListInvoices.mockResolvedValue({
      invoices: [{ id: 'i1', status: 'INVOICE_STATUS_PAID', lineItems: [], subtotalCents: BigInt(0) }],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useInvoicesTable(), { wrapper: createWrapper() })
    const data = await result.current.queryFn({ pageSize: 10 })

    expect(data.items[0].status).toBe('PAID')
  })

  it('queryFn returns empty on NotFound', async () => {
    mockListInvoices.mockRejectedValue(new ConnectError('not found', Code.NotFound))

    const { result } = renderHook(() => useInvoicesTable(), { wrapper: createWrapper() })
    expect(await result.current.queryFn({ pageSize: 10 })).toEqual({ items: [] })
  })
})

describe('useInvoiceDetail', () => {
  beforeEach(() => vi.clearAllMocks())

  it('fetches and maps invoice detail', async () => {
    mockGetInvoice.mockResolvedValue({
      invoice: {
        id: 'inv-1',
        billingRunId: 'run-1',
        partyId: 'party-1',
        invoiceNumber: 'INV-2026-00001',
        lineItems: [],
        subtotalCents: BigInt(2000),
        currency: 'USD',
        status: 'INVOICE_STATUS_DRAFT',
        dueDate: '',
        createdAt: { seconds: BigInt(1700000000), nanos: 0 },
        updatedAt: { seconds: BigInt(1700000000), nanos: 0 },
      },
    })

    const { result } = renderHook(() => useInvoiceDetail('inv-1'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.data).toBeDefined())

    expect(result.current.data?.id).toBe('inv-1')
    expect(result.current.data?.status).toBe('DRAFT')
    expect(result.current.data?.subtotalCents).toBe(2000)
  })

  it('returns null when invoice is missing', async () => {
    mockGetInvoice.mockResolvedValue({ invoice: undefined })

    const { result } = renderHook(() => useInvoiceDetail('inv-1'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toBeNull()
  })

  it('does not fetch when invoiceId is undefined', () => {
    renderHook(() => useInvoiceDetail(undefined), { wrapper: createWrapper() })
    expect(mockGetInvoice).not.toHaveBeenCalled()
  })
})

describe('useInvoiceEmails', () => {
  beforeEach(() => vi.clearAllMocks())

  it('fetches and maps invoice emails', async () => {
    mockListInvoiceEmails.mockResolvedValue({
      emails: [
        {
          idempotencyKey: 'key-1',
          templateName: 'invoice',
          toAddresses: ['test@example.com'],
          status: 'EMAIL_STATUS_DELIVERED',
          sentAt: { seconds: BigInt(1700000000), nanos: 0 },
          deliveredAt: { seconds: BigInt(1700000100), nanos: 0 },
          bounceReason: '',
        },
      ],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useInvoiceEmails('inv-1'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.data).toBeDefined())

    expect(result.current.data).toHaveLength(1)
    expect(result.current.data![0].status).toBe('DELIVERED')
    expect(result.current.data![0].toAddresses).toEqual(['test@example.com'])
    expect(result.current.data![0].bounceReason).toBeUndefined()
  })

  it('strips EMAIL_STATUS_ prefix', async () => {
    mockListInvoiceEmails.mockResolvedValue({
      emails: [{ status: 'EMAIL_STATUS_BOUNCED', bounceReason: 'invalid address', toAddresses: [] }],
      pagination: { nextPageToken: '' },
    })

    const { result } = renderHook(() => useInvoiceEmails('inv-1'), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.data).toBeDefined())
    expect(result.current.data![0].status).toBe('BOUNCED')
    expect(result.current.data![0].bounceReason).toBe('invalid address')
  })

  it('does not fetch when invoiceId is undefined', () => {
    renderHook(() => useInvoiceEmails(undefined), { wrapper: createWrapper() })
    expect(mockListInvoiceEmails).not.toHaveBeenCalled()
  })
})

describe('useResendInvoiceEmail', () => {
  beforeEach(() => vi.clearAllMocks())

  it('calls resendInvoiceEmail with invoiceId', async () => {
    mockResendInvoiceEmail.mockResolvedValue({ email: { idempotencyKey: 'k1' } })

    const { result } = renderHook(() => useResendInvoiceEmail(), {
      wrapper: createWrapper(),
    })

    await result.current.mutateAsync('inv-1')
    expect(mockResendInvoiceEmail).toHaveBeenCalledWith(
      expect.objectContaining({ invoiceId: 'inv-1' }),
    )
  })
})

describe('useMarkInvoicePaid', () => {
  beforeEach(() => vi.clearAllMocks())

  it('calls markInvoicePaid with invoiceId', async () => {
    mockMarkInvoicePaid.mockResolvedValue({ invoice: { id: 'inv-1', status: 'INVOICE_STATUS_PAID' } })

    const { result } = renderHook(() => useMarkInvoicePaid(), {
      wrapper: createWrapper(),
    })

    await result.current.mutateAsync('inv-1')
    expect(mockMarkInvoicePaid).toHaveBeenCalledWith(
      expect.objectContaining({ invoiceId: 'inv-1' }),
    )
  })
})

describe('useVoidInvoice', () => {
  beforeEach(() => vi.clearAllMocks())

  it('calls voidInvoice with invoiceId', async () => {
    mockVoidInvoice.mockResolvedValue({ invoice: { id: 'inv-1', status: 'INVOICE_STATUS_VOID' } })

    const { result } = renderHook(() => useVoidInvoice(), {
      wrapper: createWrapper(),
    })

    await result.current.mutateAsync('inv-1')
    expect(mockVoidInvoice).toHaveBeenCalledWith(
      expect.objectContaining({ invoiceId: 'inv-1' }),
    )
  })
})
