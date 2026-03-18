import { createContext, useContext, useState, useCallback, useEffect, useMemo, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAuth } from '@/contexts/auth-context'
import { DEFAULT_UI_CONFIG, type TenantThemeConfig, type TenantUIConfig } from '@/lib/tenant-ui-config'
import { applyTenantTheme, resetTheme } from '@/lib/theme-utils'
import { getTenantSlugFromSubdomain } from '@/lib/tenant-utils'

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
  applyTheme: (theme: TenantThemeConfig) => void
  tenantConfig?: TenantUIConfig
}

const TenantContext = createContext<TenantContextValue | null>(null)

export function TenantProvider({ children }: { children: ReactNode }) {
  const { claims, lens } = useAuth()
  const queryClient = useQueryClient()
  const [selectedTenant, setSelectedTenant] = useState<Tenant | null>(null)
  const [tenantTheme, setTenantTheme] = useState<TenantThemeConfig | null>(null)

  const isPlatformAdmin = lens === 'platform'

  useEffect(() => {
    if (tenantTheme) {
      applyTenantTheme(tenantTheme)
    } else {
      resetTheme()
    }
  }, [tenantTheme])

  // Reset theme only on unmount to avoid a visual flash during theme switches.
  useEffect(() => {
    return () => resetTheme()
  }, [])

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
    setTenantTheme(null)
  }, [isPlatformAdmin])

  const applyTheme = useCallback((theme: TenantThemeConfig) => {
    setTenantTheme(theme)
  }, [])

  // For tenant users, slug comes from JWT claims (x-tenant-slug) or falls back
  // to subdomain parsing. VITE_BASE_DOMAIN must match the deployment domain
  // for the subdomain fallback to work correctly.
  const tenantSlug = isPlatformAdmin
    ? selectedTenant?.slug ?? null
    : claims?.tenantId ?? getTenantSlugFromSubdomain(window.location.hostname)

  const value: TenantContextValue = useMemo(
    () => ({
      currentTenant: isPlatformAdmin ? selectedTenant : null,
      tenantSlug,
      isPlatformAdmin,
      switchTenant,
      clearTenant,
      applyTheme,
      tenantConfig: DEFAULT_UI_CONFIG,
    }),
    [isPlatformAdmin, selectedTenant, tenantSlug, switchTenant, clearTenant, applyTheme],
  )

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
