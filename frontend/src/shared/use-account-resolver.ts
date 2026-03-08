import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'

export type ResolvedAccountType = 'current' | 'internal'

export interface ResolvedAccount {
  type: ResolvedAccountType
  accountId: string
}

/**
 * Resolves an account ID to its owning service (current-account or internal-account).
 *
 * Mirrors the backend CompositeAccountValidator pattern:
 * 1. Try current-account service first (most common)
 * 2. If not found, try internal-account service
 * 3. If not found in either, return null
 */
export function useAccountResolver(accountId: string | undefined) {
  const clients = useApiClients()
  const { tenantSlug } = useTenantContext()

  return useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'account-resolve', accountId],
    queryFn: async (): Promise<ResolvedAccount | null> => {
      if (!accountId) return null

      // Try current-account first
      try {
        const response = await clients.currentAccount.retrieveCurrentAccount({
          accountId,
        })
        if (response.facility) {
          return { type: 'current', accountId }
        }
      } catch {
        // Not found or error — continue to internal-account
      }

      // Try internal-account
      try {
        const response = await clients.internalAccount.retrieveInternalAccount({
          accountId,
        })
        if (response.facility) {
          return { type: 'internal', accountId }
        }
      } catch {
        // Not found in either service
      }

      return null
    },
    enabled: !!accountId && !!tenantSlug,
    staleTime: 5 * 60 * 1000, // Cache resolution for 5 minutes
    retry: false,
  })
}
