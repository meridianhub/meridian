import { describe, it, expect, vi } from 'vitest'
import { render, screen, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TenantProvider, useTenantContext } from '@/contexts/tenant-context'
import { AuthProvider } from '@/contexts/auth-context'
import {
  createPlatformAdminToken,
  createTenantUserToken,
  createSuperAdminToken,
} from '@/test/jwt-helpers'

interface Tenant {
  id: string
  slug: string
  name: string
}

function createTestTenant(overrides?: Partial<Tenant>): Tenant {
  return {
    id: 'tenant-id-1',
    slug: 'acme-corp',
    name: 'Acme Corp',
    ...overrides,
  }
}

function createWrapper(token?: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <AuthProvider initialToken={token}>
        <QueryClientProvider client={queryClient}>
          <TenantProvider>{children}</TenantProvider>
        </QueryClientProvider>
      </AuthProvider>
    )
  }
}

describe('TenantProvider - platform admin', () => {
  it('starts with no selected tenant for platform admin', async () => {
    const token = createPlatformAdminToken()
    const Wrapper = createWrapper(token)

    const TestComponent = () => {
      const { currentTenant, tenantSlug, isPlatformAdmin } = useTenantContext()
      return (
        <div>
          <span data-testid="tenant">{currentTenant ? currentTenant.slug : 'none'}</span>
          <span data-testid="slug">{tenantSlug ?? 'none'}</span>
          <span data-testid="isPlatformAdmin">{String(isPlatformAdmin)}</span>
        </div>
      )
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    expect(screen.getByTestId('tenant').textContent).toBe('none')
    expect(screen.getByTestId('slug').textContent).toBe('none')
    expect(screen.getByTestId('isPlatformAdmin').textContent).toBe('true')
  })

  it('allows platform admin to switch tenants', async () => {
    const token = createPlatformAdminToken()
    const Wrapper = createWrapper(token)
    const tenant = createTestTenant()

    let switchFn: ((t: Tenant) => void) | null = null

    const TestComponent = () => {
      const { currentTenant, tenantSlug, switchTenant } = useTenantContext()
      switchFn = switchTenant
      return (
        <div>
          <span data-testid="tenant">{currentTenant ? currentTenant.slug : 'none'}</span>
          <span data-testid="slug">{tenantSlug ?? 'none'}</span>
        </div>
      )
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    expect(screen.getByTestId('tenant').textContent).toBe('none')

    await act(async () => {
      switchFn!(tenant)
    })

    await waitFor(() => {
      expect(screen.getByTestId('tenant').textContent).toBe('acme-corp')
      expect(screen.getByTestId('slug').textContent).toBe('acme-corp')
    })
  })

  it('allows platform admin to clear tenant selection', async () => {
    const token = createPlatformAdminToken()
    const Wrapper = createWrapper(token)
    const tenant = createTestTenant()

    let switchFn: ((t: Tenant) => void) | null = null
    let clearFn: (() => void) | null = null

    const TestComponent = () => {
      const { currentTenant, switchTenant, clearTenant } = useTenantContext()
      switchFn = switchTenant
      clearFn = clearTenant
      return (
        <span data-testid="tenant">{currentTenant ? currentTenant.slug : 'none'}</span>
      )
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    await act(async () => {
      switchFn!(tenant)
    })

    await waitFor(() => {
      expect(screen.getByTestId('tenant').textContent).toBe('acme-corp')
    })

    await act(async () => {
      clearFn!()
    })

    await waitFor(() => {
      expect(screen.getByTestId('tenant').textContent).toBe('none')
    })
  })

  it('clears tenant-scoped queries when switching tenants', async () => {
    const token = createPlatformAdminToken()
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const removeQueriesSpy = vi.spyOn(queryClient, 'removeQueries')

    const Wrapper = ({ children }: { children: React.ReactNode }) => (
      <AuthProvider initialToken={token}>
        <QueryClientProvider client={queryClient}>
          <TenantProvider>{children}</TenantProvider>
        </QueryClientProvider>
      </AuthProvider>
    )

    const tenantA = createTestTenant({ id: 'id-a', slug: 'tenant-a', name: 'Tenant A' })
    const tenantB = createTestTenant({ id: 'id-b', slug: 'tenant-b', name: 'Tenant B' })

    let switchFn: ((t: Tenant) => void) | null = null

    const TestComponent = () => {
      const { switchTenant } = useTenantContext()
      switchFn = switchTenant
      return null
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    // Switch to first tenant
    await act(async () => {
      switchFn!(tenantA)
    })

    // Switch to second tenant - should remove queries scoped to tenantA
    await act(async () => {
      switchFn!(tenantB)
    })

    expect(removeQueriesSpy).toHaveBeenCalledWith(
      expect.objectContaining({ predicate: expect.any(Function) }),
    )
  })

  it('preserves platform-scoped queries when switching tenants', async () => {
    const token = createPlatformAdminToken()
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const removeQueriesSpy = vi.spyOn(queryClient, 'removeQueries')

    const Wrapper = ({ children }: { children: React.ReactNode }) => (
      <AuthProvider initialToken={token}>
        <QueryClientProvider client={queryClient}>
          <TenantProvider>{children}</TenantProvider>
        </QueryClientProvider>
      </AuthProvider>
    )

    const tenantA = createTestTenant({ id: 'id-a', slug: 'tenant-a', name: 'Tenant A' })
    const tenantB = createTestTenant({ id: 'id-b', slug: 'tenant-b', name: 'Tenant B' })

    let switchFn: ((t: Tenant) => void) | null = null

    const TestComponent = () => {
      const { switchTenant } = useTenantContext()
      switchFn = switchTenant
      return null
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    await act(async () => {
      switchFn!(tenantA)
    })

    await act(async () => {
      switchFn!(tenantB)
    })

    // Verify platform-scoped queries (those without tenant slug at index 1) are NOT removed
    const predicate = removeQueriesSpy.mock.calls[0]?.[0]?.predicate
    expect(predicate).toBeDefined()
    if (predicate) {
      // Platform-scoped query key (no tenant slug)
      const platformQuery = { queryKey: ['platform', 'tenants'] } as Parameters<typeof predicate>[0]
      expect(predicate(platformQuery)).toBe(false)

      // Tenant-scoped query key with the previous tenant slug
      const tenantQuery = { queryKey: ['tenant', 'tenant-a', 'accounts'] } as Parameters<typeof predicate>[0]
      expect(predicate(tenantQuery)).toBe(true)

      // Query with different tenant slug should NOT be removed
      const otherTenantQuery = { queryKey: ['tenant', 'other-tenant', 'accounts'] } as Parameters<typeof predicate>[0]
      expect(predicate(otherTenantQuery)).toBe(false)
    }
  })
})

describe('TenantProvider - super admin', () => {
  it('treats super-admin as platform admin with switching capability', async () => {
    const token = createSuperAdminToken()
    const Wrapper = createWrapper(token)

    const TestComponent = () => {
      const { isPlatformAdmin } = useTenantContext()
      return <span data-testid="isPlatformAdmin">{String(isPlatformAdmin)}</span>
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    expect(screen.getByTestId('isPlatformAdmin').textContent).toBe('true')
  })
})

describe('TenantProvider - tenant admin', () => {
  it('returns fixed tenant from JWT for tenant admin', async () => {
    const token = createTenantUserToken('acme-corp')
    const Wrapper = createWrapper(token)

    const TestComponent = () => {
      const { currentTenant, tenantSlug, isPlatformAdmin } = useTenantContext()
      return (
        <div>
          <span data-testid="tenant">{currentTenant ? currentTenant.slug : 'none'}</span>
          <span data-testid="slug">{tenantSlug ?? 'none'}</span>
          <span data-testid="isPlatformAdmin">{String(isPlatformAdmin)}</span>
        </div>
      )
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    expect(screen.getByTestId('slug').textContent).toBe('acme-corp')
    expect(screen.getByTestId('isPlatformAdmin').textContent).toBe('false')
  })

  it('tenant admin cannot switch tenants - switchTenant is a no-op', async () => {
    const token = createTenantUserToken('acme-corp')
    const Wrapper = createWrapper(token)
    const anotherTenant = createTestTenant({ id: 'id-2', slug: 'other-corp', name: 'Other Corp' })

    let switchFn: ((t: Tenant) => void) | null = null

    const TestComponent = () => {
      const { tenantSlug, switchTenant } = useTenantContext()
      switchFn = switchTenant
      return <span data-testid="slug">{tenantSlug ?? 'none'}</span>
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    expect(screen.getByTestId('slug').textContent).toBe('acme-corp')

    await act(async () => {
      switchFn!(anotherTenant)
    })

    // Should NOT change - tenant admin cannot switch
    expect(screen.getByTestId('slug').textContent).toBe('acme-corp')
  })

  it('tenant admin clearTenant is a no-op', async () => {
    const token = createTenantUserToken('acme-corp')
    const Wrapper = createWrapper(token)

    let clearFn: (() => void) | null = null

    const TestComponent = () => {
      const { tenantSlug, clearTenant } = useTenantContext()
      clearFn = clearTenant
      return <span data-testid="slug">{tenantSlug ?? 'none'}</span>
    }

    await act(async () => {
      render(<TestComponent />, { wrapper: Wrapper })
    })

    expect(screen.getByTestId('slug').textContent).toBe('acme-corp')

    await act(async () => {
      clearFn!()
    })

    // Should NOT change - tenant admin cannot clear
    expect(screen.getByTestId('slug').textContent).toBe('acme-corp')
  })
})

describe('useTenantContext - safety checks', () => {
  it('throws when used outside TenantProvider', () => {
    const TestComponent = () => {
      useTenantContext()
      return null
    }

    // Suppress expected error output
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)

    expect(() => render(<TestComponent />)).toThrow(
      'useTenantContext must be used within a TenantProvider',
    )

    consoleSpy.mockRestore()
  })
})
