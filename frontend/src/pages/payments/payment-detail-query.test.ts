import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fetchPaymentDetail } from './payment-detail-query'

const mockPaymentOrder = {
  paymentOrderId: 'po-123',
  debtorAccountId: 'acc-456',
  creditorIban: 'GB29NWBK60161331926819',
  amount: '10000',
  currency: 'GBP',
  status: 'COMPLETED',
  reference: 'REF-001',
  createdAt: { seconds: 1700000000, nanos: 0 },
  sagaSteps: [],
  compensationSteps: [],
}

describe('fetchPaymentDetail', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetches payment detail and returns paymentOrder', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrder: mockPaymentOrder }),
    } as Response)

    const result = await fetchPaymentDetail('po-123')

    expect(fetch).toHaveBeenCalledWith(
      '/meridian.payment_order.v1.PaymentOrderService/RetrievePaymentOrder',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ paymentOrderId: 'po-123' }),
      },
    )
    expect(result).toEqual(mockPaymentOrder)
  })

  it('throws when response is not ok', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 404,
      json: () => Promise.resolve({}),
    } as Response)

    await expect(fetchPaymentDetail('po-missing')).rejects.toThrow(
      'Failed to fetch payment order: 404',
    )
  })

  it('throws when paymentOrder is absent in response', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({}),
    } as Response)

    await expect(fetchPaymentDetail('po-123')).rejects.toThrow('Payment order not found')
  })

  it('throws when response has 500 status', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 500,
      json: () => Promise.resolve({}),
    } as Response)

    await expect(fetchPaymentDetail('po-123')).rejects.toThrow(
      'Failed to fetch payment order: 500',
    )
  })

  it('propagates network errors from fetch', async () => {
    vi.mocked(fetch).mockRejectedValue(new Error('Network failure'))

    await expect(fetchPaymentDetail('po-123')).rejects.toThrow('Network failure')
  })

  it('sends paymentOrderId in request body', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrder: mockPaymentOrder }),
    } as Response)

    await fetchPaymentDetail('specific-order-id-999')

    const callArgs = vi.mocked(fetch).mock.calls[0]
    const body = JSON.parse(callArgs[1]?.body as string)
    expect(body).toEqual({ paymentOrderId: 'specific-order-id-999' })
  })

  it('returns payment order with sagaSteps and compensationSteps', async () => {
    const orderWithSteps = {
      ...mockPaymentOrder,
      sagaSteps: [{ stepId: 'step-1', status: 'COMPLETED', description: 'Debit account' }],
      compensationSteps: [{ stepId: 'comp-1', status: 'PENDING', description: 'Reverse debit' }],
    }

    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ paymentOrder: orderWithSteps }),
    } as Response)

    const result = await fetchPaymentDetail('po-123')

    expect(result.sagaSteps).toHaveLength(1)
    expect(result.compensationSteps).toHaveLength(1)
  })
})
