import { createContext, useContext, useState, useCallback, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAuth } from '@/contexts/auth-context'
import { DEFAULT_UI_CONFIG, type TenantUIConfig } from '@/lib/tenant-ui-config'

export interface Tenant {
  id: string
  slug: string
  name: string
}

export interface TenantContextValue {
  currentTenant: Tenant | null
  tenantSlug: string | null
  isPlatformAdmin: boolean
  switchTenant: (tenant: Tenant) => void
  clearTenant: () => void
  tenantConfig?: TenantUIConfig
}

const TenantContext = createContext<TenantContextValue | null>(null)

export function TenantProvider({ children }: { children: ReactNode }) {
  const { claims, lens } = useAuth()
  const queryClient = useQueryClient()
  const [selectedTenant, setSelectedTenant] = useState<Tenant | null>(null)

  const isPlatformAdmin = lens === 'platform'

  const switchTenant = useCallback(
    (tenant: Tenant) => {
      if (!isPlatformAdmin) return

      const previousSlug = selectedTenant?.slug
      setSelectedTenant(tenant)

      // Clear tenant-scoped queries for previous tenant
      if (previousSlug) {
        queryClient.removeQueries({
          predicate: (query) => {
            const key = query.queryKey
            return Array.isArray(key) && key[1] === previousSlug
          },
        })
      }
    },
    [isPlatformAdmin, selectedTenant, queryClient],
  )

  const clearTenant = useCallback(() => {
    if (!isPlatformAdmin) return
    setSelectedTenant(null)
  }, [isPlatformAdmin])

  // For tenant admins, tenant slug is fixed from JWT claims
  const tenantSlug = isPlatformAdmin ? selectedTenant?.slug ?? null : claims?.tenantId ?? null

  const value: TenantContextValue = {
    currentTenant: isPlatformAdmin ? selectedTenant : null,
    tenantSlug,
    isPlatformAdmin,
    switchTenant,
    clearTenant,
    tenantConfig: DEFAULT_UI_CONFIG,
  }

  return <TenantContext.Provider value={value}>{children}</TenantContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useTenantContext(): TenantContextValue {
  const ctx = useContext(TenantContext)
  if (!ctx) {
    throw new Error('useTenantContext must be used within a TenantProvider')
  }
  return ctx
}
