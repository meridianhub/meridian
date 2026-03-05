import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { TransactionStatus } from '@/api/gen/meridian/common/v1/types_pb'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { FinancialPositionLog } from '../pages/index'

/**
 * Fetches a paginated list of position logs for use with DataTable.
 */
export function usePositionLogsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantSlug
    ? [...tenantKeys.all(tenantSlug), 'positions']
    : ['positions']

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<FinancialPositionLog>> {
    const statusValue = params.filters?.status
    const response = await clients.positionKeeping.listFinancialPositionLogs({
      pageToken: params.pageToken ?? '',
      accountId: params.filters?.accountId ?? '',
      status: statusValue && !Number.isNaN(Number(statusValue)) ? (Number(statusValue) as TransactionStatus) : TransactionStatus.UNSPECIFIED,
      pagination: {
        pageSize: params.pageSize,
        pageToken: params.pageToken ?? '',
      },
    })

    return {
      items: (response.logs ?? []) as FinancialPositionLog[],
      nextPageToken: response.pagination?.nextPageToken,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single position log by ID.
 */
export function usePositionLogDetail(logId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantSlug
      ? [...tenantKeys.all(tenantSlug), 'positions', logId]
      : ['positions', logId],
    queryFn: () =>
      clients.positionKeeping.retrieveFinancialPositionLog({ logId: logId! }),
    enabled: Boolean(tenantSlug && logId),
  })
}
