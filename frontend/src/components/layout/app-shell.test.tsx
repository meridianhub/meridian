import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AppShell } from '@/components/layout/app-shell'
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
            <MemoryRouter>
              {ui}
            </MemoryRouter>
          </TooltipProvider>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

describe('AppShell', () => {
  describe('layout structure', () => {
    it('renders children in the main content area', () => {
      const token = createTenantUserToken()
      renderWithProviders(
        <AppShell>
          <div data-testid="content">Page Content</div>
        </AppShell>,
        token
      )

      expect(screen.getByTestId('content')).toBeInTheDocument()
      expect(screen.getByText('Page Content')).toBeInTheDocument()
    })

    it('renders the sidebar', () => {
      const token = createTenantUserToken()
      renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      expect(screen.getByRole('navigation')).toBeInTheDocument()
    })

    it('renders the header', () => {
      const token = createTenantUserToken()
      renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      expect(screen.getByRole('banner')).toBeInTheDocument()
    })

    it('renders main content region', () => {
      const token = createTenantUserToken()
      renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      expect(screen.getByRole('main')).toBeInTheDocument()
    })
  })

  describe('two-lens navigation for tenant user', () => {
    it('shows only tenant nav items for tenant user', () => {
      const token = createTenantUserToken()
      renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /tenant management/i })).not.toBeInTheDocument()
    })
  })

  describe('two-lens navigation for platform admin', () => {
    it('shows both tenant and platform nav items for platform admin', () => {
      const token = createPlatformAdminToken()
      renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: /tenant management/i })).toBeInTheDocument()
    })

    it('shows TenantSelector in header for platform admin', () => {
      const token = createPlatformAdminToken()
      renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      expect(screen.getByTestId('tenant-selector')).toBeInTheDocument()
    })

    it('hides TenantSelector in header for tenant user', () => {
      const token = createTenantUserToken()
      renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      expect(screen.queryByTestId('tenant-selector')).not.toBeInTheDocument()
    })
  })

  describe('mobile nav toggle', () => {
    it('toggles sidebar open/closed when menu button is clicked', async () => {
      const user = userEvent.setup()
      const token = createTenantUserToken()
      const { container } = renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      // Initially sidebar is closed on mobile (data-open="false")
      const sidebar = container.querySelector('[data-open]')
      expect(sidebar).toHaveAttribute('data-open', 'false')

      // Click menu toggle
      const menuButton = screen.getByRole('button', { name: /toggle.*menu|menu.*toggle|open.*sidebar|close.*sidebar/i })
      await user.click(menuButton)

      expect(sidebar).toHaveAttribute('data-open', 'true')
    })

    it('closes sidebar when menu button is clicked again', async () => {
      const user = userEvent.setup()
      const token = createTenantUserToken()
      const { container } = renderWithProviders(
        <AppShell>
          <div>Content</div>
        </AppShell>,
        token
      )

      const sidebar = container.querySelector('[data-open]')
      const menuButton = screen.getByRole('button', { name: /toggle.*menu|menu.*toggle|open.*sidebar|close.*sidebar/i })

      await user.click(menuButton) // open
      expect(sidebar).toHaveAttribute('data-open', 'true')

      await user.click(menuButton) // close
      expect(sidebar).toHaveAttribute('data-open', 'false')
    })
  })
})
