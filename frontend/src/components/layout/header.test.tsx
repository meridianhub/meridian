import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Header } from '@/components/layout/header'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { TooltipProvider } from '@/components/ui/tooltip'
import { createPlatformAdminToken, createTenantUserToken } from '@/test/jwt-helpers'

// Mock TenantSelector to avoid dependency on ungenerated proto clients
vi.mock('@/components/layout/tenant-selector', () => ({
  TenantSelector: () => (
    <div data-testid="tenant-selector" aria-label="Select tenant">Select Tenant</div>
  ),
}))

function renderWithProviders(ui: React.ReactElement, token?: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthProvider initialToken={token}>
        <TenantProvider>
          <TooltipProvider>
            {ui}
          </TooltipProvider>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

describe('Header', () => {
  describe('basic rendering', () => {
    it('renders the Meridian logo/brand text', () => {
      const token = createTenantUserToken()
      renderWithProviders(<Header onMenuToggle={() => {}} />, token)

      expect(screen.getByText(/meridian/i)).toBeInTheDocument()
    })

    it('renders a menu toggle button', () => {
      const token = createTenantUserToken()
      renderWithProviders(<Header onMenuToggle={() => {}} />, token)

      expect(screen.getByRole('button', { name: /toggle.*menu|menu.*toggle|open.*sidebar|close.*sidebar/i })).toBeInTheDocument()
    })

    it('calls onMenuToggle when menu button is clicked', async () => {
      const user = userEvent.setup()
      const onMenuToggle = vi.fn()
      const token = createTenantUserToken()
      renderWithProviders(<Header onMenuToggle={onMenuToggle} />, token)

      const menuButton = screen.getByRole('button', { name: /toggle.*menu|menu.*toggle|open.*sidebar|close.*sidebar/i })
      await user.click(menuButton)

      expect(onMenuToggle).toHaveBeenCalledOnce()
    })
  })

  describe('TenantSelector visibility', () => {
    it('shows TenantSelector for platform admin', () => {
      const token = createPlatformAdminToken()
      renderWithProviders(<Header onMenuToggle={() => {}} />, token)

      // Platform admin should see tenant selector
      expect(screen.getByTestId('tenant-selector')).toBeInTheDocument()
    })

    it('hides TenantSelector for tenant user', () => {
      const token = createTenantUserToken()
      renderWithProviders(<Header onMenuToggle={() => {}} />, token)

      expect(screen.queryByTestId('tenant-selector')).not.toBeInTheDocument()
    })
  })

  describe('user menu', () => {
    it('renders user menu trigger', () => {
      const token = createTenantUserToken()
      renderWithProviders(<Header onMenuToggle={() => {}} />, token)

      expect(screen.getByRole('button', { name: /user.*menu|account|profile/i })).toBeInTheDocument()
    })

    it('shows logout option in user menu when opened', async () => {
      const user = userEvent.setup()
      const token = createTenantUserToken()
      renderWithProviders(<Header onMenuToggle={() => {}} />, token)

      const userMenuButton = screen.getByRole('button', { name: /user.*menu|account|profile/i })
      await user.click(userMenuButton)

      expect(screen.getByRole('menuitem', { name: /log.*out|sign.*out/i })).toBeInTheDocument()
    })
  })

  describe('accessibility', () => {
    it('has a banner landmark role', () => {
      const token = createTenantUserToken()
      renderWithProviders(<Header onMenuToggle={() => {}} />, token)

      expect(screen.getByRole('banner')).toBeInTheDocument()
    })
  })
})
