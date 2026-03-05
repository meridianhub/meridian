import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Sidebar } from '@/components/layout/sidebar'
import type { TenantContextValue } from '@/contexts/tenant-context'

vi.mock('@/contexts/tenant-context', () => ({
  useTenantContext: vi.fn(),
}))

vi.mock('@/hooks/use-tenant-features', () => ({
  useTenantFeatures: vi.fn(),
}))

import { useTenantContext } from '@/contexts/tenant-context'
import { useTenantFeatures } from '@/hooks/use-tenant-features'
import { ALL_FEATURES } from '@/lib/tenant-ui-config'

function makeContext(overrides: Partial<TenantContextValue> = {}): TenantContextValue {
  return {
    currentTenant: null,
    tenantSlug: null,
    isPlatformAdmin: false,
    switchTenant: vi.fn(),
    clearTenant: vi.fn(),
    applyTheme: vi.fn(),
    ...overrides,
  }
}

function makeFeatures(enabledFeatures: readonly string[] = ALL_FEATURES) {
  const enabledSet = new Set(enabledFeatures)
  return {
    isFeatureEnabled: (f: string) => enabledSet.has(f),
    enabledFeatures,
    defaultFeature: enabledFeatures[0] ?? 'dashboard',
  }
}

function renderSidebar(props: React.ComponentProps<typeof Sidebar>) {
  return render(
    <MemoryRouter>
      <Sidebar {...props} />
    </MemoryRouter>,
  )
}

describe('Sidebar', () => {
  beforeEach(() => {
    vi.mocked(useTenantContext).mockReturnValue(makeContext())
    vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures())
  })

  describe('tenant lens', () => {
    it('renders all tenant nav items when all features enabled', () => {
      renderSidebar({ lens: 'tenant' })

      expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Internal Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Payments' })).toBeInTheDocument()
    })

    it('does not render platform-only nav items', () => {
      renderSidebar({ lens: 'tenant' })

      expect(screen.queryByRole('link', { name: /tenant management/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /platform monitoring/i })).not.toBeInTheDocument()
    })

    it('does not render separator between tenant and platform sections', () => {
      const { container } = renderSidebar({ lens: 'tenant' })
      // No separator role element should appear
      expect(container.querySelector('[role="separator"]')).not.toBeInTheDocument()
    })
  })

  describe('platform lens', () => {
    it('renders all tenant nav items', () => {
      renderSidebar({ lens: 'platform' })

      expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Internal Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Payments' })).toBeInTheDocument()
    })

    it('renders platform-only nav items', () => {
      renderSidebar({ lens: 'platform' })

      expect(screen.getByRole('link', { name: /tenant management/i })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: /platform monitoring/i })).toBeInTheDocument()
    })

    it('renders separator between tenant and platform sections', () => {
      const { container } = renderSidebar({ lens: 'platform' })
      expect(container.querySelector('[role="separator"]')).toBeInTheDocument()
    })
  })

  describe('active state', () => {
    it('marks the current path link as active', () => {
      renderSidebar({ lens: 'tenant', currentPath: '/' })

      const dashboardLink = screen.getByRole('link', { name: /dashboard/i })
      expect(dashboardLink).toHaveAttribute('aria-current', 'page')
    })

    it('does not mark non-current links as active', () => {
      renderSidebar({ lens: 'tenant', currentPath: '/' })

      const accountsLink = screen.getByRole('link', { name: 'Accounts' })
      expect(accountsLink).not.toHaveAttribute('aria-current', 'page')
    })

    it('marks accounts link active when on /accounts path', () => {
      renderSidebar({ lens: 'tenant', currentPath: '/accounts' })

      const accountsLink = screen.getByRole('link', { name: 'Accounts' })
      expect(accountsLink).toHaveAttribute('aria-current', 'page')
    })
  })

  describe('mobile collapsed state', () => {
    it('accepts isOpen prop and renders with open state', () => {
      renderSidebar({ lens: 'tenant', isOpen: true })
      expect(screen.getByRole('complementary')).toHaveAttribute('data-open', 'true')
    })

    it('renders with closed state when isOpen is false', () => {
      renderSidebar({ lens: 'tenant', isOpen: false })
      expect(screen.getByRole('complementary')).toHaveAttribute('data-open', 'false')
    })
  })

  describe('keyboard navigation', () => {
    it('nav links are keyboard focusable', async () => {
      renderSidebar({ lens: 'tenant' })

      const dashboardLink = screen.getByRole('link', { name: /dashboard/i })
      dashboardLink.focus()
      expect(dashboardLink).toHaveFocus()
    })
  })

  describe('navigation label', () => {
    it('has an accessible nav landmark', () => {
      renderSidebar({ lens: 'tenant' })
      expect(screen.getByRole('navigation')).toBeInTheDocument()
    })
  })

  describe('feature filtering', () => {
    it('hides nav items whose feature is disabled', () => {
      vi.mocked(useTenantFeatures).mockReturnValue(
        makeFeatures(['dashboard', 'payments']),
      )
      vi.mocked(useTenantContext).mockReturnValue(makeContext({ isPlatformAdmin: false }))

      renderSidebar({ lens: 'tenant' })

      expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Payments' })).toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Accounts' })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Ledger' })).not.toBeInTheDocument()
    })

    it('always shows items without a feature field regardless of enabled features', () => {
      vi.mocked(useTenantFeatures).mockReturnValue(
        makeFeatures(['dashboard']),
      )
      vi.mocked(useTenantContext).mockReturnValue(makeContext({ isPlatformAdmin: false }))

      renderSidebar({ lens: 'tenant' })

      // Transactions has no feature field — always visible
      expect(screen.getByRole('link', { name: 'Transactions' })).toBeInTheDocument()
    })

    it('platform admin sees all nav items regardless of enabled features', () => {
      vi.mocked(useTenantFeatures).mockReturnValue(
        makeFeatures(['dashboard']),
      )
      vi.mocked(useTenantContext).mockReturnValue(makeContext({ isPlatformAdmin: true }))

      renderSidebar({ lens: 'platform' })

      expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Ledger' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Starlark Config' })).toBeInTheDocument()
    })

    it('tenant user only sees enabled feature nav items', () => {
      vi.mocked(useTenantFeatures).mockReturnValue(
        makeFeatures(['dashboard', 'accounts', 'ledger']),
      )
      vi.mocked(useTenantContext).mockReturnValue(makeContext({ isPlatformAdmin: false }))

      renderSidebar({ lens: 'tenant' })

      expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Ledger' })).toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Payments' })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Reconciliation' })).not.toBeInTheDocument()
    })
  })
})
