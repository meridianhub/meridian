import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'

export interface PaymentOrder {
  paymentOrderId: string
  debtorAccountId: string
  creditorReference: string
  amount: string
  currency: string
  status: string
  createdAt: string | null
}

export async function fetchPayments(
  params: DataTableQueryParams,
  fetchFn: typeof fetch = fetch,
): Promise<DataTableResult<PaymentOrder>> {
  const body: Record<string, unknown> = {
    pagination: {
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
    },
  }

  if (params.filters?.status) {
    body.status = params.filters.status
  }

  const response = await fetchFn(
    '/meridian.payment_order.v1.PaymentOrderService/ListPaymentOrders',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    },
  )

  if (!response.ok) {
    throw new Error(`Failed to fetch payments: ${response.status}`)
  }

  const data = (await response.json()) as {
    paymentOrders?: PaymentOrder[]
    pagination?: { nextPageToken?: string }
  }

  return {
    items: data.paymentOrders ?? [],
    nextPageToken: data.pagination?.nextPageToken,
  }
}
