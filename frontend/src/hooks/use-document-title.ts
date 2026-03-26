import { useEffect } from 'react'
import { useAuth } from '@/contexts/auth-context'
import { useTenantInfo } from '@/hooks/use-tenant-info'
import { getTenantSlugFromSubdomain, formatSlugAsDisplayName } from '@/lib/tenant-utils'

/**
 * Sets document.title dynamically based on tenant context.
 *
 * Fallback chain: JWT display name -> API display name -> formatted slug -> "Meridian"
 *
 * @param pageTitle - Optional page-specific suffix (e.g., "Accounts")
 */
export function useDocumentTitle(pageTitle?: string) {
  const { claims } = useAuth()
  const { displayName: publicDisplayName } = useTenantInfo()

  const slug = getTenantSlugFromSubdomain(window.location.hostname)
  const tenantName = claims?.tenantDisplayName
    ?? publicDisplayName
    ?? (slug ? formatSlugAsDisplayName(slug) : null)

  useEffect(() => {
    const base = tenantName ? `${tenantName} - Operations Console` : 'Meridian Operations Console'
    document.title = pageTitle ? `${pageTitle} - ${base}` : base
  }, [tenantName, pageTitle])
}
