import { useQuery } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys, manifestKeys, referenceKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { SagaDefinition } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

/**
 * Fetches saga definitions from the current manifest for use with DataTable.
 */
export function useSagasTable() {
  const { manifestHistory } = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = [...manifestKeys.current(), tenantSlug ?? '']

  async function queryFn(
    _params: DataTableQueryParams,
  ): Promise<DataTableResult<SagaDefinition>> {
    const response = await manifestHistory.getCurrentManifest({})
    const sagas = response.version?.manifest?.sagas ?? []
    return {
      items: sagas,
      nextPageToken: undefined,
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
    queryKey: tenantKeys.saga(tenantSlug ?? '', definitionId ?? ''),
    queryFn: async () => {
      const response = await sagaRegistry.getSaga({ name: definitionId ?? '' })
      return response.saga
    },
    enabled: Boolean(definitionId),
  })
}

/**
 * Fetches a saga definition from the manifest by name.
 * Used as fallback when the saga registry does not have the saga registered.
 */
export function useManifestSaga(sagaName: string | undefined, enabled: boolean = true) {
  const { manifestHistory } = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: [...manifestKeys.current(), tenantSlug ?? '', 'saga', sagaName ?? ''],
    queryFn: async () => {
      const response = await manifestHistory.getCurrentManifest({})
      const sagas = response.version?.manifest?.sagas ?? []
      return sagas.find((s: SagaDefinition) => s.name === sagaName) ?? null
    },
    enabled: !!sagaName && enabled,
  })
}

/**
 * Fetches the active saga for a given name (platform default).
 */
export function useActiveSaga(sagaName: string | undefined, enabled: boolean = true) {
  const { sagaRegistry } = useApiClients()

  return useQuery({
    queryKey: referenceKeys.activeSaga(sagaName ?? ''),
    queryFn: async () => {
      if (!sagaName) return null
      try {
        const response = await sagaRegistry.getActiveSaga({ name: sagaName })
        return response
      } catch (error) {
        if (error instanceof ConnectError && error.code === Code.NotFound) {
          return null
        }
        throw error
      }
    },
    enabled: !!sagaName && enabled,
  })
}
