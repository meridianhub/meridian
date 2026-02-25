/**
 * Integration tests verifying the MSW + RTL + vitest-axe test infrastructure.
 *
 * These tests confirm:
 * 1. MSW intercepts Connect-ES JSON mode HTTP calls correctly
 * 2. Custom renderWithProviders wraps components in all providers
 * 3. vitest-axe accessibility audits run successfully
 */
import { describe, it, expect } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { server } from './msw-handlers'
import { axe, renderWithProviders } from './test-utils'
import { createPlatformAdminToken, createTenantUserToken } from './jwt-helpers'

// Simple test component that makes a Connect-ES style fetch call
function ConnectCallerComponent() {
  return <div role="main" aria-label="Test component">Connect caller</div>
}

describe('MSW infrastructure', () => {
  it('intercepts Connect-ES POST calls and returns default empty response', async () => {
    let intercepted = false

    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () => {
        intercepted = true
        return HttpResponse.json({ account: { id: 'acct-001', status: 'ACTIVE' } })
      }),
    )

    const response = await fetch(
      '/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ accountId: 'acct-001' }),
      },
    )

    expect(intercepted).toBe(true)
    expect(response.ok).toBe(true)
    const body = await response.json() as { account: { id: string; status: string } }
    expect(body.account.id).toBe('acct-001')
    expect(body.account.status).toBe('ACTIVE')
  })

  it('resets handlers between tests so previous overrides do not bleed through', async () => {
    // The handler from the previous test was reset by afterEach server.resetHandlers()
    const response = await fetch(
      '/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ accountId: 'acct-002' }),
      },
    )

    expect(response.ok).toBe(true)
    const body = await response.json() as Record<string, unknown>
    // Falls back to default handler which returns empty object
    expect(body).toEqual({})
  })
})

describe('renderWithProviders', () => {
  it('renders component with all provider context available', async () => {
    const token = createPlatformAdminToken()

    renderWithProviders(<ConnectCallerComponent />, { initialToken: token })

    await waitFor(() => {
      expect(screen.getByRole('main')).toBeInTheDocument()
    })
    expect(screen.getByText('Connect caller')).toBeInTheDocument()
  })

  it('renders for tenant user token', async () => {
    const token = createTenantUserToken('tenant-001')

    renderWithProviders(<ConnectCallerComponent />, { initialToken: token })

    expect(screen.getByRole('main')).toBeInTheDocument()
  })

  it('renders without token for unauthenticated state', () => {
    renderWithProviders(<ConnectCallerComponent />)

    expect(screen.getByRole('main')).toBeInTheDocument()
  })
})

describe('vitest-axe accessibility audit', () => {
  it('runs accessibility audit on a simple component and finds no violations', async () => {
    const { container } = renderWithProviders(
      <div>
        <h1>Test Page</h1>
        <nav aria-label="Main navigation">
          <a href="/home">Home</a>
        </nav>
        <main>
          <p>Page content</p>
        </main>
      </div>,
    )

    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('detects accessibility violations on non-compliant markup', async () => {
    const { container } = renderWithProviders(
      // img without alt attribute - deliberate accessibility violation for test
      <div>
        <img src="test.png" />
      </div>,
    )

    const results = await axe(container)
    // This should have violations (missing alt attribute)
    expect(results.violations.length).toBeGreaterThan(0)
  })
})
