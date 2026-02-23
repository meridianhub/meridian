import { test, expect } from './fixtures'

/**
 * Dashboard E2E tests.
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 * They do NOT require the Meridian backend — API calls will fail gracefully
 * and the dashboard renders with loading/empty states.
 *
 * Auth tokens are memory-only (not persisted). The fixtures inject auth and
 * land at / (Dashboard), so these tests do not need additional navigation.
 * Quick Actions use React Router client-side navigation (navigate()), which
 * preserves the in-memory token.
 *
 * For full integration testing with a live backend, see task 45 (CI E2E workflow).
 */
test.describe('Dashboard', () => {
  test.describe('as platform-admin', () => {
    test('renders dashboard heading', async ({ platformAdminPage: page }) => {
      await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
    })

    test('renders stat card titles', async ({ platformAdminPage: page }) => {
      await expect(page.getByText('Payment Orders')).toBeVisible()
      await expect(page.getByText('Booking Logs')).toBeVisible()
      await expect(page.getByText('Ledger Postings')).toBeVisible()
    })

    test('renders Recent Activity section', async ({ platformAdminPage: page }) => {
      await expect(page.getByRole('heading', { name: 'Recent Activity' })).toBeVisible()
    })

    test('renders Quick Actions section', async ({ platformAdminPage: page }) => {
      await expect(page.getByRole('heading', { name: 'Quick Actions' })).toBeVisible()
    })

    test('shows tenant context subtitle', async ({ platformAdminPage: page }) => {
      // Platform admin auto-selects dev-tenant in DEV mode (DevTenantAutoSelector)
      await expect(page.getByText(/Overview for dev-tenant/)).toBeVisible()
    })

    test('stat cards resolve from loading state', async ({ platformAdminPage: page }) => {
      // Wait for all loading skeletons to disappear (API succeeds or errors — both clear the skeleton)
      await expect(page.getByTestId('stat-card-skeleton')).toHaveCount(0, { timeout: 15_000 })
    })
  })

  test.describe('as tenant-user', () => {
    test('renders dashboard heading', async ({ authenticatedPage: page }) => {
      await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
    })

    test('renders stat card titles', async ({ authenticatedPage: page }) => {
      await expect(page.getByText('Payment Orders')).toBeVisible()
      await expect(page.getByText('Booking Logs')).toBeVisible()
      await expect(page.getByText('Ledger Postings')).toBeVisible()
    })

    test('renders activity feed section', async ({ authenticatedPage: page }) => {
      await expect(page.getByRole('heading', { name: 'Recent Activity' })).toBeVisible()
      // Activity feed shows items, "No recent activity", or loading — all are valid without backend
      const feed = page.getByRole('heading', { name: 'Recent Activity' }).locator('..')
      await expect(feed).toBeVisible()
    })

    test('renders quick actions section', async ({ authenticatedPage: page }) => {
      await expect(page.getByRole('heading', { name: 'Quick Actions' })).toBeVisible()
    })

    test('shows tenant context subtitle', async ({ authenticatedPage: page }) => {
      // Tenant user has tenantId = "dev-tenant" from the fixture JWT
      await expect(page.getByText(/Overview for dev-tenant/)).toBeVisible()
    })
  })

  test.describe('Quick Actions navigation', () => {
    test('View Payment Orders action navigates to /payments', async ({ authenticatedPage: page }) => {
      await page.getByRole('button', { name: 'View Payment Orders' }).click()
      await expect(page).toHaveURL('/payments')
    })

    test('View Booking Logs action navigates to /ledger', async ({ authenticatedPage: page }) => {
      await page.getByRole('button', { name: 'View Booking Logs' }).click()
      await expect(page).toHaveURL('/ledger')
    })

    test('Reconciliations action navigates to /reconciliation', async ({ authenticatedPage: page }) => {
      await page.getByRole('button', { name: 'Reconciliations' }).click()
      await expect(page).toHaveURL('/reconciliation')
    })
  })
})
