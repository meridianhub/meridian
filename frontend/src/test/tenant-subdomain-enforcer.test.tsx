/**
 * Tests for TenantSubdomainEnforcer in production-like environments
 * (non-localhost hostname). Requires module-level mocks for @/api/config.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { createPlatformAdminToken } from './jwt-helpers'
import { createTestQueryClient } from './test-utils'

// Mock isOnTenantSubdomain to return false (simulating root domain)
vi.mock('@/api/config', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/config')>()
  return {
    ...actual,
    isOnTenantSubdomain: vi.fn(() => false),
  }
})

// Import AFTER mock setup
import { TenantSubdomainEnforcer } from '@/components/routing'

// Save and restore window.location for production hostname simulation
const originalLocation = window.location

function setProductionHostname() {
  Object.defineProperty(window, 'location', {
    writable: true,
    configurable: true,
    value: {
      ...originalLocation,
      hostname: 'demo.meridianhub.cloud',
      href: 'https://demo.meridianhub.cloud/',
      assign: vi.fn(),
    },
  })
}

function restoreLocation() {
  Object.defineProperty(window, 'location', {
    writable: true,
    configurable: true,
    value: originalLocation,
  })
}

function TestWrapper({
  children,
  initialToken,
  initialPath = '/',
}: {
  children: React.ReactNode
  initialToken?: string
  initialPath?: string
}) {
  const queryClient = createTestQueryClient()
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider initialToken={initialToken}>
        <TenantProvider>
          <MemoryRouter initialEntries={[initialPath]}>{children}</MemoryRouter>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

describe('TenantSubdomainEnforcer (production hostname)', () => {
  beforeEach(() => {
    setProductionHostname()
    return () => restoreLocation()
  })

  it('shows loading state when platform admin has no tenant selected', () => {
    const token = createPlatformAdminToken()
    render(
      <TestWrapper initialToken={token}>
        <Routes>
          <Route
            path="/"
            element={
              <TenantSubdomainEnforcer>
                <div>Dashboard Content</div>
              </TenantSubdomainEnforcer>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Loading tenant context...')).toBeInTheDocument()
    expect(screen.queryByText('Dashboard Content')).not.toBeInTheDocument()
  })

  it('renders children on platform paths even without tenant context', () => {
    const token = createPlatformAdminToken()
    render(
      <TestWrapper initialToken={token} initialPath="/tenants">
        <Routes>
          <Route
            path="/tenants"
            element={
              <TenantSubdomainEnforcer>
                <div>Tenants Page</div>
              </TenantSubdomainEnforcer>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Tenants Page')).toBeInTheDocument()
    expect(screen.queryByText('Loading tenant context...')).not.toBeInTheDocument()
  })
})
