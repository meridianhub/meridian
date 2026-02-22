import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { platformKeys } from '@/lib/query-keys'

/**
 * Fetches the list of tenants from the TenantService.
 * Only meaningful for platform admins - tenant users should not call this.
 *
 * Caches for 5 minutes under the platform.tenants query key.
 */
export function useTenants() {
  const { tenant } = useApiClients()

  return useQuery({
    queryKey: platformKeys.tenants(),
    queryFn: async () => {
      const response = await tenant.listTenants({})
      return response.tenants
    },
    staleTime: 5 * 60_000,
  })
}
