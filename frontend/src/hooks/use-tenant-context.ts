import { useTenantContext } from '@/contexts/tenant-context'

export function useCurrentTenant() {
  return useTenantContext().currentTenant
}

export function useTenantSlug() {
  return useTenantContext().tenantSlug
}

export function useIsPlatformAdmin() {
  return useTenantContext().isPlatformAdmin
}

export function useSwitchTenant() {
  return useTenantContext().switchTenant
}

export function useClearTenant() {
  return useTenantContext().clearTenant
}
