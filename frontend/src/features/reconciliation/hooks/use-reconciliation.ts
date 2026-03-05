import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { ReconciliationRun } from '../pages/index'
import type { ReconciliationRunDetail } from '../pages/detail'

/**
 * Fetches a paginated list of reconciliation runs for use with DataTable.
 */
export function useReconciliationRunsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.reconciliationRuns(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<ReconciliationRun>> {
    if (!tenantSlug) return { items: [] }

    const response = await clients.accountReconciliation.listReconciliationRuns({
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
      ...(params.filters?.status ? { status: params.filters.status } : {}),
      ...(params.filters?.account_id ? { accountId: params.filters.account_id } : {}),
    })

    const items: ReconciliationRun[] = (response.runs ?? []).map((run) => ({
      runId: run.runId ?? '',
      accountId: run.accountId ?? '',
      scope: (run.scope ?? '').replace('RECONCILIATION_SCOPE_', ''),
      settlementType: (run.settlementType ?? '').replace('SETTLEMENT_TYPE_', ''),
      status: (run.status ?? '').replace('RUN_STATUS_', ''),
      varianceCount: run.varianceCount ?? 0,
      periodStart: run.periodStart ?? '',
      periodEnd: run.periodEnd ?? '',
    }))

    return {
      items,
      nextPageToken: response.nextPageToken || undefined,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single reconciliation run by ID.
 */
export function useReconciliationRunDetail(runId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.reconciliationRun(tenantSlug ?? '', runId ?? ''),
    queryFn: async (): Promise<ReconciliationRunDetail | null> => {
      const response = await clients.accountReconciliation.getReconciliationRun({
        runId: runId ?? '',
      })
      if (!response) return null
      return {
        runId: response.runId ?? '',
        accountId: response.accountId ?? '',
        scope: (response.scope ?? '').replace('RECONCILIATION_SCOPE_', ''),
        settlementType: (response.settlementType ?? '').replace('SETTLEMENT_TYPE_', ''),
        status: (response.status ?? '').replace('RUN_STATUS_', ''),
        varianceCount: response.varianceCount ?? 0,
        periodStart: response.periodStart ?? '',
        periodEnd: response.periodEnd ?? '',
      }
    },
    enabled: Boolean(tenantSlug && runId),
  })
}
