import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { Sidebar } from '@/components/layout/sidebar'
import type { TenantContextValue } from '@/contexts/tenant-context'
import type { TenantFeaturesResult } from '@/hooks/use-tenant-features'
import type { FeatureId } from '@/lib/tenant-ui-config'

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

function makeFeatures(enabledFeatures: readonly FeatureId[] = ALL_FEATURES): TenantFeaturesResult {
  const enabledSet = new Set<string>(enabledFeatures)
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
    localStorage.clear()
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

  describe('mobile focus trap', () => {
    it('moves focus into sidebar when opened on mobile', async () => {
      const onClose = vi.fn()
      renderSidebar({ lens: 'tenant', isOpen: true, onClose })

      // Focus should be inside the sidebar (complementary landmark)
      const sidebar = screen.getByRole('complementary')
      expect(sidebar.contains(document.activeElement)).toBe(true)
    })

    it('Tab key wraps from last focusable to first within sidebar', async () => {
      const user = userEvent.setup()
      const onClose = vi.fn()
      renderSidebar({ lens: 'tenant', isOpen: true, onClose })

      const sidebar = screen.getByRole('complementary')
      const focusables = sidebar.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])',
      )
      const lastFocusable = focusables[focusables.length - 1]
      lastFocusable.focus()
      expect(lastFocusable).toHaveFocus()

      await user.tab()

      // Should have wrapped back to first focusable in sidebar
      expect(sidebar.contains(document.activeElement)).toBe(true)
      expect(document.activeElement).toBe(focusables[0])
    })

    it('Shift+Tab from first focusable wraps to last within sidebar', async () => {
      const user = userEvent.setup()
      const onClose = vi.fn()
      renderSidebar({ lens: 'tenant', isOpen: true, onClose })

      const sidebar = screen.getByRole('complementary')
      const focusables = sidebar.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])',
      )
      focusables[0].focus()
      expect(focusables[0]).toHaveFocus()

      await user.tab({ shift: true })

      // Should have wrapped to last focusable in sidebar
      expect(document.activeElement).toBe(focusables[focusables.length - 1])
    })

    it('restores focus to previously focused element when sidebar closes', async () => {
      const onClose = vi.fn()
      const button = document.createElement('button')
      button.textContent = 'Menu'
      document.body.appendChild(button)
      button.focus()

      const { rerender } = render(
        <MemoryRouter>
          <Sidebar lens="tenant" isOpen={true} onClose={onClose} />
        </MemoryRouter>,
      )

      // Re-render with closed state
      rerender(
        <MemoryRouter>
          <Sidebar lens="tenant" isOpen={false} onClose={onClose} />
        </MemoryRouter>,
      )

      expect(document.activeElement).toBe(button)
      document.body.removeChild(button)
    })

    it('does not trap focus when sidebar has no onClose (desktop mode)', async () => {
      const user = userEvent.setup()
      // On desktop, sidebar renders without onClose — no focus trap
      renderSidebar({ lens: 'tenant', isOpen: false })

      const dashboardLink = screen.getByRole('link', { name: /dashboard/i })
      dashboardLink.focus()
      expect(dashboardLink).toHaveFocus()

      // Tab should move to next element freely (no wrapping enforced)
      await user.tab()
      // Focus should have moved to some other element (not stuck in sidebar trap)
      // We just verify no errors were thrown and focus moved
      expect(document.activeElement).not.toBe(dashboardLink)
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

  describe('nav group structure', () => {
    it('renders four groups: Operations, Economy, Configuration, Admin', () => {
      renderSidebar({ lens: 'tenant' })

      expect(screen.getByText('Operations')).toBeInTheDocument()
      expect(screen.getByText('Economy')).toBeInTheDocument()
      expect(screen.getByText('Configuration')).toBeInTheDocument()
      expect(screen.getByText('Admin')).toBeInTheDocument()
    })

    it('places Economy items under Economy group', () => {
      renderSidebar({ lens: 'tenant' })

      const economyGroup = screen.getByRole('button', { name: /economy/i }).closest('li')!
      expect(within(economyGroup).getByRole('link', { name: 'Overview' })).toBeInTheDocument()
      expect(within(economyGroup).getByRole('link', { name: 'Reference Data' })).toBeInTheDocument()
      expect(within(economyGroup).getByRole('link', { name: 'Starlark Config' })).toBeInTheDocument()
      expect(within(economyGroup).getByRole('link', { name: 'Market Data' })).toBeInTheDocument()
      expect(within(economyGroup).getByRole('link', { name: 'Forecasting' })).toBeInTheDocument()
    })

    it('places remaining config items under Configuration group', () => {
      renderSidebar({ lens: 'tenant' })

      const configGroup = screen.getByText('Configuration').closest('li')!
      expect(within(configGroup).getByRole('link', { name: 'Gateway Mappings' })).toBeInTheDocument()
      expect(within(configGroup).getByRole('link', { name: 'MCP Config' })).toBeInTheDocument()
      expect(within(configGroup).getByRole('link', { name: 'Cookbook' })).toBeInTheDocument()
    })
  })

  describe('collapsible Economy group', () => {
    it('renders Economy header as a button with aria-expanded', () => {
      renderSidebar({ lens: 'tenant' })

      const economyButton = screen.getByRole('button', { name: /economy/i })
      expect(economyButton).toBeInTheDocument()
      expect(economyButton).toHaveAttribute('aria-expanded', 'true')
    })

    it('collapses Economy items when toggle button is clicked', async () => {
      const user = userEvent.setup()
      renderSidebar({ lens: 'tenant' })

      const economyButton = screen.getByRole('button', { name: /economy/i })
      await user.click(economyButton)

      expect(economyButton).toHaveAttribute('aria-expanded', 'false')
      expect(screen.queryByRole('link', { name: 'Overview' })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Reference Data' })).not.toBeInTheDocument()
    })

    it('expands Economy items when toggle is clicked again', async () => {
      const user = userEvent.setup()
      renderSidebar({ lens: 'tenant' })

      const economyButton = screen.getByRole('button', { name: /economy/i })
      await user.click(economyButton) // collapse
      await user.click(economyButton) // expand

      expect(economyButton).toHaveAttribute('aria-expanded', 'true')
      expect(screen.getByRole('link', { name: 'Overview' })).toBeInTheDocument()
    })

    it('persists collapsed state to localStorage', async () => {
      const user = userEvent.setup()
      renderSidebar({ lens: 'tenant' })

      const economyButton = screen.getByRole('button', { name: /economy/i })
      await user.click(economyButton)

      const stored = JSON.parse(localStorage.getItem('meridian:sidebar-collapsed') ?? '[]')
      expect(stored).toContain('Economy')
    })

    it('restores collapsed state from localStorage', () => {
      localStorage.setItem('meridian:sidebar-collapsed', JSON.stringify(['Economy']))
      renderSidebar({ lens: 'tenant' })

      const economyButton = screen.getByRole('button', { name: /economy/i })
      expect(economyButton).toHaveAttribute('aria-expanded', 'false')
      expect(screen.queryByRole('link', { name: 'Overview' })).not.toBeInTheDocument()
    })

    it('auto-expands Economy when currentPath matches an Economy child route', () => {
      localStorage.setItem('meridian:sidebar-collapsed', JSON.stringify(['Economy']))
      renderSidebar({ lens: 'tenant', currentPath: '/economy' })

      const economyButton = screen.getByRole('button', { name: /economy/i })
      expect(economyButton).toHaveAttribute('aria-expanded', 'true')
      expect(screen.getByRole('link', { name: 'Overview' })).toBeInTheDocument()
    })

    it('auto-expands Economy when currentPath is /reference-data', () => {
      localStorage.setItem('meridian:sidebar-collapsed', JSON.stringify(['Economy']))
      renderSidebar({ lens: 'tenant', currentPath: '/reference-data' })

      expect(screen.getByRole('button', { name: /economy/i })).toHaveAttribute('aria-expanded', 'true')
    })

    it('auto-expands Economy when currentPath is /starlark-config', () => {
      localStorage.setItem('meridian:sidebar-collapsed', JSON.stringify(['Economy']))
      renderSidebar({ lens: 'tenant', currentPath: '/starlark-config' })

      expect(screen.getByRole('button', { name: /economy/i })).toHaveAttribute('aria-expanded', 'true')
    })

    it('does not auto-expand Economy for unrelated paths', () => {
      localStorage.setItem('meridian:sidebar-collapsed', JSON.stringify(['Economy']))
      renderSidebar({ lens: 'tenant', currentPath: '/accounts' })

      expect(screen.getByRole('button', { name: /economy/i })).toHaveAttribute('aria-expanded', 'false')
    })

    it('non-collapsible groups render header as static text, not button', () => {
      renderSidebar({ lens: 'tenant' })

      // Operations is not collapsible — no button for it
      expect(screen.queryByRole('button', { name: /operations/i })).not.toBeInTheDocument()
      expect(screen.getByText('Operations')).toBeInTheDocument()
    })
  })

  describe('Economy group feature gate', () => {
    it('hides entire Economy group when economy feature is disabled', () => {
      vi.mocked(useTenantFeatures).mockReturnValue(
        makeFeatures(['dashboard', 'accounts']),
      )
      vi.mocked(useTenantContext).mockReturnValue(makeContext({ isPlatformAdmin: false }))

      renderSidebar({ lens: 'tenant' })

      expect(screen.queryByRole('button', { name: /economy/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Overview' })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Reference Data' })).not.toBeInTheDocument()
    })

    it('shows Economy group for platform admin even when economy feature disabled', () => {
      vi.mocked(useTenantFeatures).mockReturnValue(
        makeFeatures(['dashboard']),
      )
      vi.mocked(useTenantContext).mockReturnValue(makeContext({ isPlatformAdmin: true }))

      renderSidebar({ lens: 'platform' })

      expect(screen.getByRole('button', { name: /economy/i })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Overview' })).toBeInTheDocument()
    })

    it('hides individual Economy items whose specific feature is disabled', () => {
      vi.mocked(useTenantFeatures).mockReturnValue(
        makeFeatures(['dashboard', 'economy', 'reference-data']),
      )
      vi.mocked(useTenantContext).mockReturnValue(makeContext({ isPlatformAdmin: false }))

      renderSidebar({ lens: 'tenant' })

      // Economy group visible (economy feature enabled)
      expect(screen.getByRole('button', { name: /economy/i })).toBeInTheDocument()
      // Overview visible (economy feature)
      expect(screen.getByRole('link', { name: 'Overview' })).toBeInTheDocument()
      // Reference Data visible (reference-data feature)
      expect(screen.getByRole('link', { name: 'Reference Data' })).toBeInTheDocument()
      // Starlark Config hidden (sagas feature not enabled)
      expect(screen.queryByRole('link', { name: 'Starlark Config' })).not.toBeInTheDocument()
      // Market Data hidden
      expect(screen.queryByRole('link', { name: 'Market Data' })).not.toBeInTheDocument()
    })
  })
})
