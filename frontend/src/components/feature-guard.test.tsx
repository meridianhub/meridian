import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { FeatureGuard } from '@/components/feature-guard'
import type { TenantFeaturesResult } from '@/hooks/use-tenant-features'
import type { FeatureId } from '@/lib/tenant-ui-config'

vi.mock('@/hooks/use-tenant-features', () => ({
  useTenantFeatures: vi.fn(),
}))

import { useTenantFeatures } from '@/hooks/use-tenant-features'

function makeFeatures(enabled: readonly FeatureId[]): TenantFeaturesResult {
  const enabledSet = new Set<string>(enabled)
  return {
    isFeatureEnabled: (feature: string) => enabledSet.has(feature),
    enabledFeatures: enabled,
    defaultFeature: enabled[0] ?? 'dashboard',
  }
}

function renderGuard(feature: FeatureId, isEnabled: boolean, fallback?: string) {
  vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures(isEnabled ? [feature] : []))

  return render(
    <MemoryRouter initialEntries={[`/${feature}`]}>
      <FeatureGuard feature={feature} fallback={fallback}>
        <div>Protected content</div>
      </FeatureGuard>
    </MemoryRouter>,
  )
}

describe('FeatureGuard', () => {
  it('renders children when feature is enabled', () => {
    renderGuard('accounts', true)
    expect(screen.getByText('Protected content')).toBeInTheDocument()
  })

  it('redirects to "/" by default when feature is disabled', () => {
    vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures([]))

    render(
      <MemoryRouter initialEntries={['/accounts']}>
        <Routes>
          <Route
            path="/accounts"
            element={
              <FeatureGuard feature="accounts">
                <div>Protected content</div>
              </FeatureGuard>
            }
          />
          <Route path="/" element={<div>Home fallback</div>} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.queryByText('Protected content')).not.toBeInTheDocument()
    expect(screen.getByText('Home fallback')).toBeInTheDocument()
  })

  it('redirects to custom fallback when feature is disabled', () => {
    vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures(['dashboard']))

    render(
      <MemoryRouter initialEntries={['/payments']}>
        <Routes>
          <Route
            path="/payments"
            element={
              <FeatureGuard feature="payments" fallback="/dashboard">
                <div>Protected content</div>
              </FeatureGuard>
            }
          />
          <Route path="/dashboard" element={<div>Dashboard fallback</div>} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.queryByText('Protected content')).not.toBeInTheDocument()
    expect(screen.getByText('Dashboard fallback')).toBeInTheDocument()
  })

  it('renders children when multiple features are enabled', () => {
    vi.mocked(useTenantFeatures).mockReturnValue(
      makeFeatures(['dashboard', 'accounts', 'payments']),
    )

    render(
      <MemoryRouter>
        <FeatureGuard feature="payments">
          <div>Payments content</div>
        </FeatureGuard>
      </MemoryRouter>,
    )

    expect(screen.getByText('Payments content')).toBeInTheDocument()
  })

  it('redirects when one feature among many is disabled', () => {
    vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures(['dashboard', 'accounts']))

    render(
      <MemoryRouter>
        <FeatureGuard feature="payments">
          <div>Payments content</div>
        </FeatureGuard>
      </MemoryRouter>,
    )

    expect(screen.queryByText('Payments content')).not.toBeInTheDocument()
  })
})
