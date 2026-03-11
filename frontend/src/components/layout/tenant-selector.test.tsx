import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders, axe } from '@/test/test-utils'
import { createPlatformAdminToken } from '@/test/jwt-helpers'
import { TenantSelector } from './tenant-selector'

// Mock useTenants so we control what data is returned
vi.mock('@/hooks/use-tenants', () => ({
  useTenants: vi.fn(),
}))

// Mock useTenantContext so we control currentTenant and switchTenant
vi.mock('@/contexts/tenant-context', () => ({
  useTenantContext: vi.fn(),
  TenantProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useTenants } from '@/hooks/use-tenants'
import { useTenantContext } from '@/contexts/tenant-context'

const mockTenants = [
  {
    tenantId: 'acme_corp',
    slug: 'acme-bank',
    displayName: 'Acme Bank',
    status: 1,
    settlementAsset: 'GBP',
    subdomain: '',
    partyId: '',
    errorMessage: '',
    version: 1,
    createdAt: undefined,
    deprovisionedAt: undefined,
    metadata: undefined,
  },
  {
    tenantId: 'beta_corp',
    slug: 'beta-financial',
    displayName: 'Beta Financial',
    status: 1,
    settlementAsset: 'USD',
    subdomain: '',
    partyId: '',
    errorMessage: '',
    version: 1,
    createdAt: undefined,
    deprovisionedAt: undefined,
    metadata: undefined,
  },
  {
    tenantId: 'gamma_inc',
    slug: 'gamma-energy',
    displayName: 'Gamma Energy',
    status: 1,
    settlementAsset: 'EUR',
    subdomain: '',
    partyId: '',
    errorMessage: '',
    version: 1,
    createdAt: undefined,
    deprovisionedAt: undefined,
    metadata: undefined,
  },
]

const mockSwitchTenant = vi.fn()

function setupMocks(overrides: {
  tenants?: typeof mockTenants
  isLoading?: boolean
  currentTenantSlug?: string | null
} = {}) {
  const { tenants = mockTenants, isLoading = false, currentTenantSlug = null } = overrides

  vi.mocked(useTenants).mockReturnValue({
    data: isLoading ? undefined : tenants,
    isLoading,
    isError: false,
    error: null,
  } as unknown as ReturnType<typeof useTenants>)

  vi.mocked(useTenantContext).mockReturnValue({
    tenantSlug: currentTenantSlug,
    currentTenant: currentTenantSlug
      ? tenants.find((t) => t.slug === currentTenantSlug) ?? null
      : null,
    isPlatformAdmin: true,
    switchTenant: mockSwitchTenant,
    clearTenant: vi.fn(),
  })
}

describe('TenantSelector', () => {
  const token = createPlatformAdminToken()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering', () => {
    it('renders with data-testid tenant-selector', () => {
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      expect(screen.getByTestId('tenant-selector')).toBeInTheDocument()
    })

    it('shows "Select tenant..." placeholder when no tenant is selected', () => {
      setupMocks({ currentTenantSlug: null })
      renderWithProviders(<TenantSelector />, { initialToken: token })

      expect(screen.getByText(/select tenant/i)).toBeInTheDocument()
    })

    it('shows current tenant display name when a tenant is selected', () => {
      setupMocks({ currentTenantSlug: 'acme-bank' })
      renderWithProviders(<TenantSelector />, { initialToken: token })

      expect(screen.getByText('Acme Bank')).toBeInTheDocument()
    })

    it('shows loading state when tenants are loading', () => {
      setupMocks({ isLoading: true })
      renderWithProviders(<TenantSelector />, { initialToken: token })

      expect(screen.getByTestId('tenant-selector-loading')).toBeInTheDocument()
    })
  })

  describe('interaction', () => {
    it('opens dropdown when button is clicked', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByRole('listbox')).toBeInTheDocument()
      })
    })

    it('lists all tenants in the dropdown', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByText('Acme Bank')).toBeInTheDocument()
        expect(screen.getByText('Beta Financial')).toBeInTheDocument()
        expect(screen.getByText('Gamma Energy')).toBeInTheDocument()
      })
    })

    it('shows tenant slugs in the dropdown', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByText('acme-bank')).toBeInTheDocument()
      })
    })

    it('calls switchTenant with the correct tenant object when a tenant is selected', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByText('Acme Bank')).toBeInTheDocument()
      })

      await user.click(screen.getByText('Acme Bank'))

      expect(mockSwitchTenant).toHaveBeenCalledWith({
        id: 'acme_corp',
        slug: 'acme-bank',
        name: 'Acme Bank',
      })
    })

    it('closes dropdown after tenant selection', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByText('Acme Bank')).toBeInTheDocument()
      })

      await user.click(screen.getByText('Acme Bank'))

      await waitFor(() => {
        expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
      })
    })

    it('shows checkmark next to currently selected tenant', async () => {
      const user = userEvent.setup()
      setupMocks({ currentTenantSlug: 'acme-bank' })
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        // The selected tenant item should have aria-selected=true
        const selectedItem = screen.getByRole('option', { name: /acme bank/i })
        expect(selectedItem).toHaveAttribute('aria-selected', 'true')
      })
    })

    it('excludes deprovisioned tenants from the dropdown', async () => {
      const user = userEvent.setup()
      const tenantsWithDeprovisioned = [
        ...mockTenants,
        {
          tenantId: 'decom_corp',
          slug: 'decom-corp',
          displayName: 'Decommissioned Corp',
          status: 3, // DEPROVISIONED = 3
          settlementAsset: 'GBP',
          subdomain: '',
          partyId: '',
          errorMessage: '',
          version: 1,
          createdAt: undefined,
          deprovisionedAt: undefined,
          metadata: undefined,
        },
      ]
      setupMocks({ tenants: tenantsWithDeprovisioned })
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByText('Acme Bank')).toBeInTheDocument()
        expect(screen.getByText('Beta Financial')).toBeInTheDocument()
        expect(screen.getByText('Gamma Energy')).toBeInTheDocument()
        expect(screen.queryByText('Decommissioned Corp')).not.toBeInTheDocument()
      })
    })
  })

  describe('search', () => {
    it('filters tenants by display name', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      const searchInput = await screen.findByPlaceholderText(/search tenants/i)
      await user.type(searchInput, 'acme')

      await waitFor(() => {
        expect(screen.getByText('Acme Bank')).toBeInTheDocument()
        expect(screen.queryByText('Beta Financial')).not.toBeInTheDocument()
        expect(screen.queryByText('Gamma Energy')).not.toBeInTheDocument()
      })
    })

    it('filters tenants by slug', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      const searchInput = await screen.findByPlaceholderText(/search tenants/i)
      await user.type(searchInput, 'beta-financial')

      await waitFor(() => {
        expect(screen.getByText('Beta Financial')).toBeInTheDocument()
        expect(screen.queryByText('Acme Bank')).not.toBeInTheDocument()
      })
    })

    it('shows empty state when no tenants match search', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      const searchInput = await screen.findByPlaceholderText(/search tenants/i)
      await user.type(searchInput, 'nonexistent-tenant-xyz')

      await waitFor(() => {
        expect(screen.getByText(/no tenants found/i)).toBeInTheDocument()
      })
    })
  })

  describe('keyboard navigation', () => {
    it('opens dropdown on Enter key', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      button.focus()
      await user.keyboard('{Enter}')

      await waitFor(() => {
        expect(screen.getByRole('listbox')).toBeInTheDocument()
      })
    })

    it('closes dropdown on Escape key', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByRole('listbox')).toBeInTheDocument()
      })

      await user.keyboard('{Escape}')

      await waitFor(() => {
        expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
      })
    })
  })

  describe('empty state', () => {
    it('shows empty state when no tenants exist', async () => {
      const user = userEvent.setup()
      setupMocks({ tenants: [] })
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByText(/no tenants found/i)).toBeInTheDocument()
      })
    })
  })

  describe('accessibility', () => {
    it('has no accessibility violations in closed state', async () => {
      setupMocks()
      const { container } = renderWithProviders(<TenantSelector />, { initialToken: token })

      const results = await axe(container)
      expect(results).toHaveNoViolations()
    })

    it('trigger button has accessible label', () => {
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      expect(button).toHaveAccessibleName()
    })

    it('announces selection changes to screen readers', async () => {
      const user = userEvent.setup()
      setupMocks()
      renderWithProviders(<TenantSelector />, { initialToken: token })

      const button = screen.getByRole('combobox')
      await user.click(button)

      await waitFor(() => {
        expect(screen.getByRole('listbox')).toBeInTheDocument()
      })

      // The listbox should have aria-label
      expect(screen.getByRole('listbox')).toHaveAccessibleName()
    })
  })
})
