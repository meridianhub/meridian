import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { ProtectedRoute, PlatformOnlyRoute, TenantSubdomainEnforcer } from '@/components/routing'
import {
  createTestToken,
  createPlatformAdminToken,
  createTenantUserToken,
} from './jwt-helpers'
import { createTestQueryClient } from './test-utils'

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

describe('ProtectedRoute', () => {
  it('redirects to /login when unauthenticated', () => {
    render(
      <TestWrapper>
        <Routes>
          <Route path="/login" element={<div>Login Page</div>} />
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <div>Protected Content</div>
              </ProtectedRoute>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Login Page')).toBeInTheDocument()
    expect(screen.queryByText('Protected Content')).not.toBeInTheDocument()
  })

  it('renders children when authenticated', () => {
    const token = createTenantUserToken()
    render(
      <TestWrapper initialToken={token}>
        <Routes>
          <Route path="/login" element={<div>Login Page</div>} />
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <div>Protected Content</div>
              </ProtectedRoute>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Protected Content')).toBeInTheDocument()
    expect(screen.queryByText('Login Page')).not.toBeInTheDocument()
  })

  it('redirects to /login with expired token', () => {
    // Expired token - create one that's already expired
    const expiredToken = createTestToken({
      userId: 'user-123',
      exp: Math.floor(Date.now() / 1000) - 3600,
      tenantId: 'tenant-abc',
    })
    render(
      <TestWrapper initialToken={expiredToken}>
        <Routes>
          <Route path="/login" element={<div>Login Page</div>} />
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <div>Protected Content</div>
              </ProtectedRoute>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Login Page')).toBeInTheDocument()
    expect(screen.queryByText('Protected Content')).not.toBeInTheDocument()
  })
})

describe('TenantSubdomainEnforcer', () => {
  it('renders children on localhost (local dev bypass)', () => {
    // On localhost, isLocalDev() returns true, the enforcer is fully bypassed
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

    expect(screen.getByText('Dashboard Content')).toBeInTheDocument()
    expect(screen.queryByText('Loading tenant context...')).not.toBeInTheDocument()
  })

  it('renders children on platform paths without tenant context', () => {
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

describe('PlatformOnlyRoute', () => {
  it('redirects to / when user is tenant admin', () => {
    const token = createTenantUserToken()
    render(
      <TestWrapper initialToken={token} initialPath="/tenants">
        <Routes>
          <Route path="/" element={<div>Dashboard</div>} />
          <Route
            path="/tenants"
            element={
              <PlatformOnlyRoute>
                <div>Tenant Management</div>
              </PlatformOnlyRoute>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Dashboard')).toBeInTheDocument()
    expect(screen.queryByText('Tenant Management')).not.toBeInTheDocument()
  })

  it('renders children when user is platform admin', () => {
    const token = createPlatformAdminToken()
    render(
      <TestWrapper initialToken={token} initialPath="/tenants">
        <Routes>
          <Route path="/" element={<div>Dashboard</div>} />
          <Route
            path="/tenants"
            element={
              <PlatformOnlyRoute>
                <div>Tenant Management</div>
              </PlatformOnlyRoute>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Tenant Management')).toBeInTheDocument()
    expect(screen.queryByText('Dashboard')).not.toBeInTheDocument()
  })

  it('redirects to / when unauthenticated', () => {
    render(
      <TestWrapper initialPath="/tenants">
        <Routes>
          <Route path="/" element={<div>Dashboard</div>} />
          <Route
            path="/tenants"
            element={
              <PlatformOnlyRoute>
                <div>Tenant Management</div>
              </PlatformOnlyRoute>
            }
          />
        </Routes>
      </TestWrapper>,
    )

    expect(screen.getByText('Dashboard')).toBeInTheDocument()
    expect(screen.queryByText('Tenant Management')).not.toBeInTheDocument()
  })
})
