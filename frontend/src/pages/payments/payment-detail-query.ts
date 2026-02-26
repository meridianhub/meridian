import type { SagaStep } from '@/components/shared/saga-timeline'

export interface PaymentOrderDetail {
  paymentOrderId: string
  debtorAccountId: string
  creditorReference: string
  amount: string
  currency: string
  status: string
  reference?: string
  createdAt: { seconds: bigint | number; nanos?: number } | null
  sagaSteps: SagaStep[]
  compensationSteps: SagaStep[]
}

export async function fetchPaymentDetail(
  paymentOrderId: string,
): Promise<PaymentOrderDetail> {
  const response = await fetch(
    '/meridian.payment_order.v1.PaymentOrderService/RetrievePaymentOrder',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paymentOrderId }),
    },
  )

  if (!response.ok) {
    throw new Error(`Failed to fetch payment order: ${response.status}`)
  }

  const data = (await response.json()) as {
    paymentOrder?: PaymentOrderDetail
  }

  if (!data.paymentOrder) {
    throw new Error('Payment order not found')
  }

  return data.paymentOrder
}
