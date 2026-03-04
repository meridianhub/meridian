import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fetchPayments } from './payments-query'
import type { DataTableQueryParams } from '@/shared/data-table'

const mockPaymentOrders = [
  {
    paymentOrderId: 'po-001',
    debtorAccountId: 'acc-100',
    creditorReference: 'GB29NWBK60161331926819',
    amount: '5000',
    currency: 'GBP',
    status: 'COMPLETED',
    createdAt: '2023-11-14T22:13:20Z',
  },
  {
    paymentOrderId: 'po-002',
    debtorAccountId: 'acc-200',
    creditorReference: 'DE89370400440532013000',
    amount: '15000',
    currency: 'EUR',
    status: 'PENDING',
    createdAt: '2023-11-14T22:30:00Z',
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
      json: () => Promise.resolve({
        paymentOrders: mockPaymentOrders,
        pagination: { nextPageToken: 'token-2' },
      }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    const result = await fetchPayments(params)

    expect(fetch).toHaveBeenCalledWith(
      '/meridian.payment_order.v1.PaymentOrderService/ListPaymentOrders',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pagination: { pageSize: 20, pageToken: '' } }),
      },
    )
    expect(result.items).toEqual(mockPaymentOrders)
    expect(result.nextPageToken).toBe('token-2')
  })

  it('includes pageToken in pagination when provided', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 10, pageToken: 'cursor-abc' }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body.pagination.pageToken).toBe('cursor-abc')
  })

  it('sends empty pageToken when not provided', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 10 }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body.pagination.pageToken).toBe('')
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

  it('reads nextPageToken from pagination in response', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: mockPaymentOrders, pagination: { nextPageToken: 'page-2' } }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    const result = await fetchPayments(params)

    expect(result.nextPageToken).toBe('page-2')
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

  it('returns undefined nextPageToken when pagination is absent', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: mockPaymentOrders }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 20 }
    const result = await fetchPayments(params)

    expect(result.nextPageToken).toBeUndefined()
  })

  it('passes pageSize correctly in pagination object', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrders: [] }),
    } as Response)

    const params: DataTableQueryParams = { pageSize: 50 }
    await fetchPayments(params)

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body.pagination.pageSize).toBe(50)
  })
})
