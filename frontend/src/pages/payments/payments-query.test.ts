import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fetchPayments } from './payments-query'
import type { DataTableQueryParams } from '@/components/shared/data-table'

const mockPaymentOrders = [
  {
    paymentOrderId: 'po-001',
    debtorAccountId: 'acc-100',
    creditorReference: 'GB29NWBK60161331926819',
    amount: '5000',
    currency: 'GBP',
    status: 'COMPLETED',
    createdAt: { seconds: 1700000000, nanos: 0 },
  },
  {
    paymentOrderId: 'po-002',
    debtorAccountId: 'acc-200',
    creditorReference: 'DE89370400440532013000',
    amount: '15000',
    currency: 'EUR',
    status: 'PENDING',
    createdAt: { seconds: 1700001000, nanos: 0 },
  },
]

describe('fetchPayments', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetches payments and returns items with nextPageToken', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: mockPaymentOrders, nextPageToken: 'token-2' }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    const result = await fetchPayments(params)

    expect(fetch).toHaveBeenCalledWith(
      '/meridian.payment_order.v1.PaymentOrderService/ListPaymentOrders',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pageSize: 20 }),
      },
    )
    expect(result.items).toEqual(mockPaymentOrders)
    expect(result.nextPageToken).toBe('token-2')
  })

  it('includes pageToken in request body when provided', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [], nextPageToken: undefined }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 10, pageToken: 'cursor-abc' }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body.pageToken).toBe('cursor-abc')
  })

  it('does not include pageToken when not provided', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 10 }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body).not.toHaveProperty('pageToken')
  })

  it('includes status filter in request body when provided', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 10, filters: { status: 'COMPLETED' } }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body.status).toBe('COMPLETED')
  })

  it('does not include status in request body when filters is absent', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 10 }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body).not.toHaveProperty('status')
  })

  it('does not include status when filters.status is absent', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 10, filters: { otherFilter: 'value' } }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body).not.toHaveProperty('status')
  })

  it('returns empty items array when paymentOrders is absent', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({}),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    const result = await fetchPayments(params)

    expect(result.items).toEqual([])
    expect(result.nextPageToken).toBeUndefined()
  })

  it('throws when response is not ok', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 403,
      json: () => Promise.resolve({}),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    await expect(fetchPayments(params)).rejects.toThrow('Failed to fetch payments: 403')
  })

  it('throws when response has 500 status', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 500,
      json: () => Promise.resolve({}),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    await expect(fetchPayments(params)).rejects.toThrow('Failed to fetch payments: 500')
  })

  it('propagates network errors from fetch', async () => {
    vi.mocked(fetch).mockRejectedValue(new Error('Network failure'))

    const params: DataTableQueryParams = { pageSize: 20 }
    await expect(fetchPayments(params)).rejects.toThrow('Network failure')
  })

  it('returns undefined nextPageToken when not in response', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: mockPaymentOrders }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    const result = await fetchPayments(params)

    expect(result.nextPageToken).toBeUndefined()
  })

  it('passes pageSize correctly in request body', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 50 }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body.pageSize).toBe(50)
  })
})
