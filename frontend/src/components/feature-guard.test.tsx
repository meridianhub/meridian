import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { FeatureGuard } from '@/components/feature-guard'

vi.mock('@/hooks/use-tenant-features', () => ({
  useTenantFeatures: vi.fn(),
}))

import { useTenantFeatures } from '@/hooks/use-tenant-features'

function makeFeatures(enabled: string[]) {
  return {
    isFeatureEnabled: (feature: string) => enabled.includes(feature),
    enabledFeatures: enabled as never,
    defaultFeature: enabled[0] ?? 'dashboard',
  }
}

function renderGuard(feature: string, isEnabled: boolean, fallback?: string) {
  vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures(isEnabled ? [feature] : []))

  return render(
    <MemoryRouter initialEntries={[`/${feature}`]}>
      <FeatureGuard feature={feature as never} fallback={fallback}>
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
    // Render with a route structure that can show the redirect target
    vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures([]))

    render(
      <MemoryRouter initialEntries={['/accounts']}>
        <FeatureGuard feature="accounts">
          <div>Protected content</div>
        </FeatureGuard>
      </MemoryRouter>,
    )

    expect(screen.queryByText('Protected content')).not.toBeInTheDocument()
  })

  it('redirects to custom fallback when feature is disabled', () => {
    vi.mocked(useTenantFeatures).mockReturnValue(makeFeatures(['dashboard']))

    // Render with routes to verify the redirect target
    render(
      <MemoryRouter initialEntries={['/payments']}>
        <FeatureGuard feature="payments" fallback="/dashboard">
          <div>Protected content</div>
        </FeatureGuard>
      </MemoryRouter>,
    )

    expect(screen.queryByText('Protected content')).not.toBeInTheDocument()
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
