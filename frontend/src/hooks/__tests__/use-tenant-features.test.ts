import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useTenantFeatures, ALL_FEATURES } from '../use-tenant-features'
import type { TenantContextValue } from '@/contexts/tenant-context'
import { DEFAULT_UI_CONFIG } from '@/lib/tenant-ui-config'

vi.mock('@/contexts/tenant-context', () => ({
  useTenantContext: vi.fn(),
}))

import { useTenantContext } from '@/contexts/tenant-context'

function makeContext(overrides: Partial<TenantContextValue> = {}): TenantContextValue {
  return {
    currentTenant: null,
    tenantSlug: null,
    isPlatformAdmin: false,
    switchTenant: vi.fn(),
    clearTenant: vi.fn(),
    ...overrides,
  }
}

describe('useTenantFeatures', () => {
  it('returns all features enabled when no tenantConfig is set', () => {
    vi.mocked(useTenantContext).mockReturnValue(makeContext())

    const { result } = renderHook(() => useTenantFeatures())

    expect(result.current.enabledFeatures).toEqual([...ALL_FEATURES])
    expect(result.current.defaultFeature).toBe('dashboard')
  })

  it('returns all features enabled when using DEFAULT_UI_CONFIG', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({ tenantConfig: DEFAULT_UI_CONFIG }),
    )

    const { result } = renderHook(() => useTenantFeatures())

    expect(result.current.enabledFeatures).toEqual([...ALL_FEATURES])
    ALL_FEATURES.forEach((f) => {
      expect(result.current.isFeatureEnabled(f)).toBe(true)
    })
  })

  it('correctly filters disabled features when config limits enabled list', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({
        tenantConfig: {
          features: {
            enabled: ['dashboard', 'accounts', 'payments'],
            defaultFeature: 'dashboard',
          },
        },
      }),
    )

    const { result } = renderHook(() => useTenantFeatures())

    expect(result.current.enabledFeatures).toEqual(['dashboard', 'accounts', 'payments'])
    expect(result.current.isFeatureEnabled('dashboard')).toBe(true)
    expect(result.current.isFeatureEnabled('accounts')).toBe(true)
    expect(result.current.isFeatureEnabled('payments')).toBe(true)
    expect(result.current.isFeatureEnabled('ledger')).toBe(false)
    expect(result.current.isFeatureEnabled('tenants')).toBe(false)
  })

  it('isFeatureEnabled returns false for unknown feature names', () => {
    vi.mocked(useTenantContext).mockReturnValue(makeContext({ tenantConfig: DEFAULT_UI_CONFIG }))

    const { result } = renderHook(() => useTenantFeatures())

    expect(result.current.isFeatureEnabled('nonexistent-feature')).toBe(false)
  })

  it('uses custom defaultFeature from config', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({
        tenantConfig: {
          features: {
            enabled: ['accounts', 'payments'],
            defaultFeature: 'accounts',
          },
        },
      }),
    )

    const { result } = renderHook(() => useTenantFeatures())

    expect(result.current.defaultFeature).toBe('accounts')
  })

  it('falls back to dashboard as defaultFeature when config has no defaultFeature', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({
        tenantConfig: {
          features: {
            enabled: ['dashboard', 'accounts'],
          },
        },
      }),
    )

    const { result } = renderHook(() => useTenantFeatures())

    expect(result.current.defaultFeature).toBe('dashboard')
  })

  it('falls back gracefully when tenantConfig has no features key', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({
        tenantConfig: {},
      }),
    )

    const { result } = renderHook(() => useTenantFeatures())

    expect(result.current.enabledFeatures).toEqual([...ALL_FEATURES])
    expect(result.current.defaultFeature).toBe('dashboard')
    expect(result.current.isFeatureEnabled('dashboard')).toBe(true)
  })
})
