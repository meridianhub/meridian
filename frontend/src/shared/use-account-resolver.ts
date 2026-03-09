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
 *
 * When the caller has already checked one service (e.g. AccountDetailPage already
 * tried current-account via useAccountDetail), pass skipServices to avoid redundant calls.
 */
export function useAccountResolver(
  accountId: string | undefined,
  options?: { skipServices?: ResolvedAccountType[] },
) {
  const clients = useApiClients()
  const { tenantSlug } = useTenantContext()
  const skip = options?.skipServices ?? []

  return useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'account-resolve', accountId, skip],
    queryFn: async (): Promise<ResolvedAccount | null> => {
      if (!accountId) return null

      // Try current-account
      if (!skip.includes('current')) {
        try {
          const response = await clients.currentAccount.retrieveCurrentAccount({
            accountId,
          })
          if (response.facility) {
            return { type: 'current', accountId }
          }
        } catch {
          // Not found or error — continue
        }
      }

      // Try internal-account
      if (!skip.includes('internal')) {
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
      }

      return null
    },
    enabled: !!accountId && !!tenantSlug,
    staleTime: 5 * 60 * 1000, // Cache resolution for 5 minutes
    retry: false,
  })
}
