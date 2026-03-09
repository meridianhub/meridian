import { describe, it, expect } from 'vitest'
import { screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createPlatformAdminToken, createTestToken } from '@/test/jwt-helpers'
import { AdminOnlyRoute } from '@/components/routing'

function renderWithRoute(token: string) {
  return renderWithProviders(
    <MemoryRouter initialEntries={['/admin']}>
      <Routes>
        <Route
          path="/admin"
          element={
            <AdminOnlyRoute>
              <div data-testid="admin-content">Admin Content</div>
            </AdminOnlyRoute>
          }
        />
        <Route path="/" element={<div data-testid="home">Home</div>} />
      </Routes>
    </MemoryRouter>,
    { initialToken: token },
  )
}

describe('AdminOnlyRoute', () => {
  it('renders children for platform admin', () => {
    renderWithRoute(createPlatformAdminToken())
    expect(screen.getByTestId('admin-content')).toBeInTheDocument()
  })

  it('redirects non-admin users to home', () => {
    const viewerToken = createTestToken({ roles: ['viewer'] })
    renderWithRoute(viewerToken)
    expect(screen.getByTestId('home')).toBeInTheDocument()
  })

  it('renders children for tenant-admin', () => {
    const tenantAdminToken = createTestToken({
      roles: ['tenant-admin'],
      tenantId: 'test-tenant',
    })
    renderWithRoute(tenantAdminToken)
    expect(screen.getByTestId('admin-content')).toBeInTheDocument()
  })

  it('renders children for super-admin', () => {
    const superAdminToken = createTestToken({ roles: ['super-admin'] })
    renderWithRoute(superAdminToken)
    expect(screen.getByTestId('admin-content')).toBeInTheDocument()
  })
})
