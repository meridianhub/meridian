import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'

// Mock the tenant context so we can control return values without needing
// the full provider tree (AuthProvider, QueryClient, etc.).
const mockContextValue = {
  currentTenant: { id: 'tenant-1', slug: 'acme', name: 'Acme Corp' },
  tenantSlug: 'acme',
  isPlatformAdmin: false,
  switchTenant: vi.fn(),
  clearTenant: vi.fn(),
  applyTheme: vi.fn(),
}

vi.mock('@/contexts/tenant-context', () => ({
  useTenantContext: vi.fn(() => mockContextValue),
}))

import { useTenantContext } from '@/contexts/tenant-context'
import {
  useCurrentTenant,
  useTenantSlug,
  useIsPlatformAdmin,
  useSwitchTenant,
  useClearTenant,
} from './use-tenant-context'

function wrapper({ children }: { children: ReactNode }) {
  return <>{children}</>
}

describe('use-tenant-context hooks', () => {
  beforeEach(() => {
    vi.mocked(useTenantContext).mockReturnValue(mockContextValue)
  })

  it('useCurrentTenant returns currentTenant from context', () => {
    const { result } = renderHook(() => useCurrentTenant(), { wrapper })
    expect(result.current).toEqual({ id: 'tenant-1', slug: 'acme', name: 'Acme Corp' })
  })

  it('useTenantSlug returns tenantSlug from context', () => {
    const { result } = renderHook(() => useTenantSlug(), { wrapper })
    expect(result.current).toBe('acme')
  })

  it('useIsPlatformAdmin returns isPlatformAdmin from context', () => {
    const { result } = renderHook(() => useIsPlatformAdmin(), { wrapper })
    expect(result.current).toBe(false)
  })

  it('useIsPlatformAdmin returns true for platform admins', () => {
    vi.mocked(useTenantContext).mockReturnValue({
      ...mockContextValue,
      isPlatformAdmin: true,
    })

    const { result } = renderHook(() => useIsPlatformAdmin(), { wrapper })
    expect(result.current).toBe(true)


  })

  it('useSwitchTenant returns switchTenant function from context', () => {
    const { result } = renderHook(() => useSwitchTenant(), { wrapper })
    expect(result.current).toBe(mockContextValue.switchTenant)
  })

  it('useClearTenant returns clearTenant function from context', () => {
    const { result } = renderHook(() => useClearTenant(), { wrapper })
    expect(result.current).toBe(mockContextValue.clearTenant)
  })

  it('useCurrentTenant returns null when no tenant selected', () => {
    vi.mocked(useTenantContext).mockReturnValue({
      ...mockContextValue,
      currentTenant: null,
      tenantSlug: null,
    })

    const { result } = renderHook(() => useCurrentTenant(), { wrapper })
    expect(result.current).toBeNull()

    vi.mocked(useTenantContext).mockReturnValue(mockContextValue)
  })

  it('useTenantSlug returns null when no tenant selected', () => {
    vi.mocked(useTenantContext).mockReturnValue({
      ...mockContextValue,
      currentTenant: null,
      tenantSlug: null,
    })

    const { result } = renderHook(() => useTenantSlug(), { wrapper })
    expect(result.current).toBeNull()

    vi.mocked(useTenantContext).mockReturnValue(mockContextValue)
  })
})
