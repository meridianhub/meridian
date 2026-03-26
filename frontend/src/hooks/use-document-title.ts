import { useEffect } from 'react'
import { useAuth } from '@/contexts/auth-context'
import { useTenantInfo } from '@/hooks/use-tenant-info'

/**
 * Sets document.title dynamically based on tenant context.
 *
 * When authenticated: uses the tenant display name from the JWT claim.
 * When on login page (pre-auth): uses the tenant info from the public API.
 * Falls back to "Meridian Operations Console" on bare domain.
 *
 * @param pageTitle - Optional page-specific suffix (e.g., "Accounts")
 */
export function useDocumentTitle(pageTitle?: string) {
  const { claims } = useAuth()
  const { displayName: publicDisplayName } = useTenantInfo()

  // Prefer JWT claim (authenticated), fall back to public API (pre-auth)
  const tenantName = claims?.tenantDisplayName ?? publicDisplayName

  useEffect(() => {
    const base = tenantName ? `${tenantName} - Operations Console` : 'Meridian Operations Console'
    document.title = pageTitle ? `${pageTitle} - ${base}` : base
  }, [tenantName, pageTitle])
}
