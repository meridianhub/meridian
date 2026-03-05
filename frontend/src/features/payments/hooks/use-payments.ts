import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { PaymentOrder } from '../pages/payments-query'
import type { PaymentOrderDetail } from '../pages/payment-detail-query'

/**
 * Fetches a paginated list of payment orders for use with DataTable.
 * Returns the queryKey and queryFn ready to pass to DataTable.
 */
export function usePaymentsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.payments(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<PaymentOrder>> {
    if (!tenantSlug) return { items: [] }

    const response = await clients.paymentOrder.listPaymentOrders({
      pagination: {
        pageSize: params.pageSize,
        pageToken: params.pageToken ?? '',
      },
      ...(params.filters?.status ? { status: params.filters.status } : {}),
    })

    const items: PaymentOrder[] = (response.paymentOrders ?? []).map((p) => ({
      paymentOrderId: p.paymentOrderId ?? '',
      debtorAccountId: p.debtorAccountId ?? '',
      creditorReference: p.creditorReference ?? '',
      amount: p.amount ?? '',
      currency: p.currency ?? '',
      status: p.status ?? '',
      createdAt: p.createdAt ?? null,
    }))

    return {
      items,
      nextPageToken: response.pagination?.nextPageToken || undefined,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single payment order by ID.
 */
export function usePaymentDetail(paymentOrderId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.payment(tenantSlug ?? '', paymentOrderId ?? ''),
    queryFn: async (): Promise<PaymentOrderDetail | null> => {
      const response = await clients.paymentOrder.retrievePaymentOrder({
        paymentOrderId: paymentOrderId ?? '',
      })
      const p = response.paymentOrder
      if (!p) return null
      return p as unknown as PaymentOrderDetail
    },
    enabled: Boolean(tenantSlug && paymentOrderId),
  })
}
