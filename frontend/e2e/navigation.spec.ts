import { test, expect } from './fixtures'

/**
 * Navigation smoke tests.
 *
 * Verifies that all sidebar links load their respective pages without crashing.
 * Tests run against the Vite dev server only — no backend required.
 *
 * Active-link highlighting is tested by checking aria-current="page" on the
 * active nav link, which is set in sidebar.tsx based on currentPath.
 */

// Routes rendered as tenant-nav items in sidebar.tsx, plus the routes they render
const TENANT_ROUTES = [
  { path: '/', label: 'Dashboard', heading: /^Dashboard$/ },
  { path: '/accounts', label: 'Accounts', heading: /^Accounts$/ },
  { path: '/internal-accounts', label: 'Internal Accounts', heading: /^Internal Accounts$/ },
  { path: '/payments', label: 'Payments', heading: /^Payments$/ },
  { path: '/positions', label: 'Positions', heading: /^Positions$/ },
  { path: '/ledger', label: 'Ledger', heading: /^Ledger$/ },
  { path: '/parties', label: 'Parties', heading: /^Parties$/ },
  { path: '/reconciliation', label: 'Reconciliation', heading: /^Reconciliation$/ },
  { path: '/starlark-config', label: 'Starlark Config', heading: /Starlark Configuration/i },
  { path: '/market-data', label: 'Market Data', heading: /^Market Data$/ },
  { path: '/forecasting', label: 'Forecasting', heading: /^Forecasting$/ },
  { path: '/reference-data', label: 'Reference Data', heading: /^Reference Data$/ },
  { path: '/gateway-mappings', label: 'Gateway Mappings', heading: /Gateway Mappings/i },
  { path: '/manifests', label: 'Manifests', heading: /Manifest Configuration/i },
  { path: '/audit-log', label: 'Audit Log', heading: /^Audit Log$/ },
] as const

test.describe('Sidebar navigation - tenant-user', () => {
  for (const route of TENANT_ROUTES) {
    test(`${route.path} renders without error`, async ({ authenticatedPage: page }) => {
      await page.goto(route.path)

      // Page must have a body with content
      await expect(page.locator('body')).not.toBeEmpty()

      // Should not show a generic error boundary
      await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()

      // Should have a visible h1 matching the expected heading
      await expect(
        page.getByRole('heading', { level: 1 }).filter({ hasText: route.heading }),
      ).toBeVisible({ timeout: 10_000 })
    })
  }
})

test.describe('Sidebar navigation - platform-admin', () => {
  const PLATFORM_ROUTES = [
    { path: '/tenants', heading: /Tenant/i },
    { path: '/platform', heading: /Platform/i },
  ] as const

  for (const route of PLATFORM_ROUTES) {
    test(`${route.path} renders without error`, async ({ platformAdminPage: page }) => {
      await page.goto(route.path)

      await expect(page.locator('body')).not.toBeEmpty()
      await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
      await expect(
        page.getByRole('heading', { level: 1 }).filter({ hasText: route.heading }),
      ).toBeVisible({ timeout: 10_000 })
    })
  }
})

test.describe('Active link highlighting', () => {
  test('Dashboard link is active on /', async ({ authenticatedPage: page }) => {
    await page.goto('/')
    const nav = page.getByRole('navigation', { name: 'Main navigation' })
    const dashboardLink = nav.getByRole('link', { name: 'Dashboard' })

    await expect(dashboardLink).toHaveAttribute('aria-current', 'page')
    await expect(dashboardLink).toHaveClass(/bg-gray-700/)
  })

  test('Accounts link is active on /accounts', async ({ authenticatedPage: page }) => {
    await page.goto('/accounts')
    const nav = page.getByRole('navigation', { name: 'Main navigation' })
    const accountsLink = nav.getByRole('link', { name: 'Accounts' })

    await expect(accountsLink).toHaveAttribute('aria-current', 'page')
    await expect(accountsLink).toHaveClass(/bg-gray-700/)
  })

  test('only the active link has aria-current="page"', async ({ authenticatedPage: page }) => {
    await page.goto('/accounts')
    const nav = page.getByRole('navigation', { name: 'Main navigation' })

    // Dashboard link must NOT be active
    const dashboardLink = nav.getByRole('link', { name: 'Dashboard' })
    await expect(dashboardLink).not.toHaveAttribute('aria-current', 'page')

    // Accounts link must be active
    const accountsLink = nav.getByRole('link', { name: 'Accounts' })
    await expect(accountsLink).toHaveAttribute('aria-current', 'page')
  })

  test('active link updates after sidebar navigation', async ({ authenticatedPage: page }) => {
    await page.goto('/')

    const nav = page.getByRole('navigation', { name: 'Main navigation' })

    // Start: Dashboard active
    await expect(nav.getByRole('link', { name: 'Dashboard' })).toHaveAttribute(
      'aria-current',
      'page',
    )

    // Navigate to Payments via sidebar link
    await nav.getByRole('link', { name: 'Payments' }).click()
    await expect(page).toHaveURL('/payments')

    // After navigation: Payments active, Dashboard inactive
    await expect(nav.getByRole('link', { name: 'Payments' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    await expect(nav.getByRole('link', { name: 'Dashboard' })).not.toHaveAttribute(
      'aria-current',
      'page',
    )
  })
})

test.describe('Tenant context preservation across navigation', () => {
  test('tenant subtitle persists after navigating away from Dashboard and back', async ({
    authenticatedPage: page,
  }) => {
    // Start on Dashboard — tenant subtitle visible
    await expect(page.getByText(/Overview for dev-tenant/)).toBeVisible()

    // Navigate to Accounts
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Accounts' }).click()
    await expect(page).toHaveURL('/accounts')

    // Navigate back to Dashboard
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Dashboard' }).click()
    await expect(page).toHaveURL('/')

    // Tenant context (tenantSlug) must still be visible
    await expect(page.getByText(/Overview for dev-tenant/)).toBeVisible()
  })
})

test.describe('Error handling', () => {
  test('404 page renders for unknown routes', async ({ authenticatedPage: page }) => {
    await page.goto('/this-route-does-not-exist-12345')

    await expect(page.getByText(/404/)).toBeVisible()
    await expect(page.getByText(/Page Not Found/i)).toBeVisible()
  })
})

test.describe('Mobile sidebar', () => {
  test('sidebar is initially closed on mobile viewport', async ({ authenticatedPage: page }) => {
    await page.setViewportSize({ width: 375, height: 812 })
    await page.goto('/')

    const sidebar = page.locator('#app-sidebar')
    await expect(sidebar).toHaveAttribute('data-open', 'false')
  })

  test('menu toggle opens and closes the sidebar', async ({ authenticatedPage: page }) => {
    await page.setViewportSize({ width: 375, height: 812 })
    await page.goto('/')

    const sidebar = page.locator('#app-sidebar')
    const toggle = page.getByRole('button', { name: 'Toggle menu' })

    // Initially closed
    await expect(sidebar).toHaveAttribute('data-open', 'false')

    // Open
    await toggle.click()
    await expect(sidebar).toHaveAttribute('data-open', 'true')

    // Close
    await toggle.click()
    await expect(sidebar).toHaveAttribute('data-open', 'false')
  })
})
