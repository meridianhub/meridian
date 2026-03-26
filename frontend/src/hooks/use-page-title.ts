import { useContext, useEffect } from 'react'
import { AuthContext } from '@/contexts/auth-context'

export function usePageTitle(title: string) {
  // useContext returns null when AuthProvider is absent (e.g., standalone test renders)
  const auth = useContext(AuthContext)
  const tenantName = auth?.claims?.tenantDisplayName

  useEffect(() => {
    const base = tenantName ? `${tenantName} - Operations Console` : 'Meridian Operations Console'
    document.title = title ? `${title} - ${base}` : base
  }, [title, tenantName])
}
