import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'

interface DataSetRow {
  id: string
  code: string
  displayName: string
  category: number
  unit: string
  status: number
  createdAt?: { seconds: bigint | number; nanos?: number } | null
}

/**
 * Fetches a paginated list of market data sets for use with DataTable.
 */
export function useDatasetsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.marketDataSets(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<DataSetRow>> {
    if (!tenantSlug) return { items: [] }

    const statusFilter = params.filters?.status ? parseInt(params.filters.status, 10) : 0
    const categoryFilter = params.filters?.category ? parseInt(params.filters.category, 10) : 0

    const res = await clients.marketInformation.listDataSets({
      statusFilter,
      categoryFilter,
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
    })

    return {
      items: res.datasets.map((d) => ({
        id: d.id,
        code: d.code,
        displayName: d.displayName,
        category: d.category,
        unit: d.unit,
        status: d.status,
        createdAt: d.createdAt ?? null,
      })),
      nextPageToken: res.nextPageToken || undefined,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single market data set by code.
 */
export function useDatasetDetail(datasetCode: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.marketDataSet(tenantSlug ?? '', datasetCode ?? ''),
    queryFn: () =>
      clients.marketInformation.retrieveDataSet({
        code: datasetCode!,
        version: 0,
      }),
    enabled: Boolean(tenantSlug && datasetCode),
  })
}

/**
 * Fetches observations for a dataset.
 */
export function useDatasetObservations(datasetCode: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: [...tenantKeys.marketDataSet(tenantSlug ?? '', datasetCode ?? ''), 'observations'],
    queryFn: () =>
      clients.marketInformation.listObservations({
        datasetCode: datasetCode!,
        pageSize: 100,
        pageToken: '',
      }),
    enabled: Boolean(tenantSlug && datasetCode),
  })
}
