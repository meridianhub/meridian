import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { SagaDefinition } from '@/api/gen/meridian/saga/v1/saga_registry_pb'

/**
 * Fetches a paginated list of saga definitions for use with DataTable.
 */
export function useSagasTable() {
  const { sagaRegistry } = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.sagas(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<SagaDefinition>> {
    const response = await sagaRegistry.listSagas({
      pageSize: params.pageSize,
      pageToken: params.pageToken,
    })
    return {
      items: response.sagas ?? [],
      nextPageToken: response.nextPageToken || undefined,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single saga definition by ID.
 */
export function useSagaDetail(definitionId: string | undefined) {
  const { sagaRegistry } = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantSlug
      ? [...tenantKeys.sagas(tenantSlug), definitionId]
      : ['starlark-config', definitionId],
    queryFn: async () => {
      const response = await sagaRegistry.getSaga({ id: definitionId ?? '' })
      return response.saga
    },
    enabled: Boolean(tenantSlug && definitionId),
  })
}

/**
 * Fetches the active saga for a given name (platform default).
 */
export function useActiveSaga(sagaName: string | undefined, enabled: boolean = true) {
  const { sagaRegistry } = useApiClients()

  return useQuery({
    queryKey: ['starlark-config', 'active', sagaName],
    queryFn: async () => {
      if (!sagaName) return null
      const response = await sagaRegistry.getActiveSaga({ name: sagaName })
      return response
    },
    enabled: !!sagaName && enabled,
  })
}
