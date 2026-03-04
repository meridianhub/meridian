import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useTenantLayout } from '../use-tenant-layout'
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

describe('useTenantLayout', () => {
  it('returns empty widgets and tableDefaults when no tenantConfig is set', () => {
    vi.mocked(useTenantContext).mockReturnValue(makeContext())

    const { result } = renderHook(() => useTenantLayout())

    expect(result.current.widgets).toEqual([])
    expect(result.current.tableDefaults).toEqual({})
  })

  it('returns empty widgets and tableDefaults with DEFAULT_UI_CONFIG (no layout defined)', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({ tenantConfig: DEFAULT_UI_CONFIG }),
    )

    const { result } = renderHook(() => useTenantLayout())

    expect(result.current.widgets).toEqual([])
    expect(result.current.tableDefaults).toEqual({})
  })

  it('returns widgets from tenant layout config', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({
        tenantConfig: {
          layout: {
            dashboard: {
              widgets: [
                { feature: 'payments', component: 'StatCards', position: 0 },
                { feature: 'ledger', component: 'ActivityFeed', position: 1 },
              ],
            },
            tableDefaults: {},
          },
        },
      }),
    )

    const { result } = renderHook(() => useTenantLayout())

    expect(result.current.widgets).toHaveLength(2)
    expect(result.current.widgets[0]).toEqual({
      feature: 'payments',
      component: 'StatCards',
      position: 0,
    })
    expect(result.current.widgets[1]).toEqual({
      feature: 'ledger',
      component: 'ActivityFeed',
      position: 1,
    })
  })

  it('returns tableDefaults from tenant layout config', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({
        tenantConfig: {
          layout: {
            dashboard: { widgets: [] },
            tableDefaults: {
              payments: { visibleColumns: ['id', 'amount', 'status'], defaultSort: 'createdAt' },
              accounts: { visibleColumns: ['id', 'name'] },
            },
          },
        },
      }),
    )

    const { result } = renderHook(() => useTenantLayout())

    expect(result.current.tableDefaults).toEqual({
      payments: { visibleColumns: ['id', 'amount', 'status'], defaultSort: 'createdAt' },
      accounts: { visibleColumns: ['id', 'name'] },
    })
  })

  it('getTableDefaults returns config for a known table key', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({
        tenantConfig: {
          layout: {
            dashboard: { widgets: [] },
            tableDefaults: {
              payments: { visibleColumns: ['id', 'amount'], defaultSort: 'id' },
            },
          },
        },
      }),
    )

    const { result } = renderHook(() => useTenantLayout())

    expect(result.current.getTableDefaults('payments')).toEqual({
      visibleColumns: ['id', 'amount'],
      defaultSort: 'id',
    })
  })

  it('getTableDefaults returns undefined for an unknown table key', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({ tenantConfig: DEFAULT_UI_CONFIG }),
    )

    const { result } = renderHook(() => useTenantLayout())

    expect(result.current.getTableDefaults('nonexistent')).toBeUndefined()
  })

  it('falls back gracefully when tenantConfig has no layout key', () => {
    vi.mocked(useTenantContext).mockReturnValue(
      makeContext({ tenantConfig: {} }),
    )

    const { result } = renderHook(() => useTenantLayout())

    expect(result.current.widgets).toEqual([])
    expect(result.current.tableDefaults).toEqual({})
    expect(result.current.getTableDefaults('anything')).toBeUndefined()
  })
})
