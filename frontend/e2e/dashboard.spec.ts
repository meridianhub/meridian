import { test, expect } from './fixtures'

/**
 * Smoke test: verify the dashboard renders after dev-mode authentication.
 *
 * NOTE: This test requires the Vite dev server (npm run dev) to be running.
 * It does NOT require the Meridian backend - API calls will fail gracefully
 * and the dashboard will render with loading/error states.
 *
 * For full integration testing with live backend, see task 45 (CI E2E workflow).
 */
test.describe('Dashboard smoke test', () => {
  test('renders dashboard heading after platform-admin login', async ({ platformAdminPage }) => {
    // Wait on the UI landmark rather than URL to avoid redirect path mismatches
    await expect(platformAdminPage.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
  })

  test('renders stat card section', async ({ platformAdminPage }) => {
    // Stat cards are always rendered (loading or data states).
    // Use exact: true to avoid matching partial text in Quick Actions buttons.
    await expect(platformAdminPage.getByText('Payment Orders', { exact: true })).toBeVisible()
    await expect(platformAdminPage.getByText('Booking Logs', { exact: true })).toBeVisible()
    await expect(platformAdminPage.getByText('Ledger Postings', { exact: true })).toBeVisible()
  })

  test('renders quick actions panel', async ({ platformAdminPage }) => {
    await expect(platformAdminPage.getByText('Quick Actions')).toBeVisible()
  })
})
