import { test, expect, navigateTo } from './fixtures'

/**
 * Positions page E2E tests.
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 *
 * Auth tokens are memory-only (not persisted). All tests use navigateTo() for
 * client-side navigation to preserve in-memory auth state.
 *
 * Navigation, page structure, and UI rendering tests do NOT require the
 * Meridian backend — the positions page renders with an empty/error state when
 * no backend is available.
 *
 * Tests that require real position data (quality ladder, row click navigation)
 * are guarded by MERIDIAN_E2E_BACKEND=1 since they depend on existing records.
 *
 * Tests use `authenticatedPage` (tenant-user role) since positions are
 * tenant-scoped resources.
 */

test.describe('Positions page', () => {
  test('renders Positions heading after navigation', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions')
    await expect(authenticatedPage.getByRole('heading', { name: 'Positions' })).toBeVisible()
  })

  test('renders positions page subtitle', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions')
    await expect(
      authenticatedPage.getByText('Financial position logs with bi-temporal data quality tracking.'),
    ).toBeVisible()
  })

  test('renders positions table column headers', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions')
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Log ID' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Account' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Status' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Created' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Last Updated' })).toBeVisible()
  })

  test('renders Account ID filter input', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions')
    await expect(authenticatedPage.getByPlaceholder('Account ID')).toBeVisible()
  })

  test('renders Status filter select', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions')
    await expect(authenticatedPage.getByLabel('Status')).toBeVisible()
  })
})

test.describe('Position detail page', () => {
  test('renders Position Log heading for unknown ID', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions/non-existent-log-id')
    await expect(
      authenticatedPage.getByRole('heading', { name: 'Position Log' }),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('renders breadcrumb back link on position detail page', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions/non-existent-log-id')
    await expect(
      authenticatedPage.getByRole('link', { name: 'Positions' }),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('breadcrumb back link navigates to positions list', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions/non-existent-log-id')
    await expect(
      authenticatedPage.getByRole('link', { name: 'Positions' }),
    ).toBeVisible({ timeout: 10_000 })
    await authenticatedPage.getByRole('link', { name: 'Positions' }).click()
    await expect(authenticatedPage.getByRole('heading', { name: 'Positions' })).toBeVisible()
  })
})

/**
 * Full data tests — require live backend with seeded position log records.
 * Un-skip when running against a full stack (task 45 CI workflow).
 */
test.describe('Positions page with data (requires backend)', () => {
  test.skip(
    process.env.MERIDIAN_E2E_BACKEND !== '1',
    'Set MERIDIAN_E2E_BACKEND=1 to run data-dependent tests',
  )

  test('displays position log rows in the table', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    const rows = authenticatedPage.locator('[data-testid="data-table-row"]')
    expect(await rows.count()).toBeGreaterThan(0)
  })

  test('navigates to position detail on row click', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/positions')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(authenticatedPage).toHaveURL(/\/positions\/[a-zA-Z0-9_-]+/)
    await expect(
      authenticatedPage.getByRole('heading', { name: 'Position Log' }),
    ).toBeVisible()
  })

  test('position detail shows balance view with provisional and available balances', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/positions')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(authenticatedPage.getByTestId('provisional-balance')).toBeVisible({ timeout: 10_000 })
    await expect(authenticatedPage.getByTestId('available-balance')).toBeVisible()
  })

  test('position detail Measurement History tab renders quality ladder badges', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/positions')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await authenticatedPage.getByRole('tab', { name: 'Measurement History' }).click()
    const badges = authenticatedPage.locator('[data-testid="quality-ladder-badge"]')
    await expect(badges.first()).toBeVisible({ timeout: 10_000 })
    const badgeCount = await badges.count()
    expect(badgeCount).toBeGreaterThan(0)
  })

  test('position detail Measurement History tab renders measurement entries', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/positions')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await authenticatedPage.getByRole('tab', { name: 'Measurement History' }).click()
    const historyTable = authenticatedPage.getByTestId('measurement-history-table')
    await expect(historyTable).toBeVisible({ timeout: 10_000 })
    const entries = authenticatedPage.locator('[data-testid="measurement-entry"]')
    expect(await entries.count()).toBeGreaterThan(0)
  })
})
