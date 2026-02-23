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
    // Auth injection navigates to '/' which redirects to ProtectedRoute -> dashboard
    await platformAdminPage.waitForURL('/')

    // Dashboard heading is the primary landmark
    await expect(platformAdminPage.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
  })

  test('renders stat card section', async ({ platformAdminPage }) => {
    await platformAdminPage.waitForURL('/')

    // Stat cards are always rendered (loading or data states)
    await expect(platformAdminPage.getByText('Payment Orders')).toBeVisible()
    await expect(platformAdminPage.getByText('Booking Logs')).toBeVisible()
    await expect(platformAdminPage.getByText('Ledger Postings')).toBeVisible()
  })

  test('renders quick actions panel', async ({ platformAdminPage }) => {
    await platformAdminPage.waitForURL('/')

    await expect(platformAdminPage.getByText('Quick Actions')).toBeVisible()
  })
})
