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
      const first = await tenant.listTenants({})
      let all = first.tenants ?? []
      let pageToken = first.nextPageToken
      while (pageToken) {
        const page = await tenant.listTenants({ pageToken })
        all = all.concat(page.tenants ?? [])
        pageToken = page.nextPageToken
      }
      return all
    },
    staleTime: 5 * 60_000,
  })
}
