import { test, expect, navigateTo } from './fixtures'

/**
 * Ledger page E2E tests.
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 *
 * Auth tokens are memory-only (not persisted). All tests use navigateTo() for
 * client-side navigation to preserve in-memory auth state.
 *
 * Navigation, page structure, and UI rendering tests do NOT require the
 * Meridian backend — the ledger page renders with an empty/error state when
 * no backend is available.
 *
 * Tests that require real booking log data (postings table, balance indicator,
 * row click navigation) are guarded by MERIDIAN_E2E_BACKEND=1.
 *
 * Tests use `authenticatedPage` (tenant-user role) since ledger data is
 * tenant-scoped.
 */

test.describe('Ledger page', () => {
  test('renders Ledger heading after navigation', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await expect(authenticatedPage.getByRole('heading', { name: 'Ledger' })).toBeVisible()
  })

  test('renders ledger page subtitle', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await expect(
      authenticatedPage.getByText('Financial booking logs and double-entry postings'),
    ).toBeVisible()
  })

  test('renders booking logs table column headers', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Log ID' })).toBeVisible()
    await expect(
      authenticatedPage.getByRole('columnheader', { name: 'Account Type' }),
    ).toBeVisible()
    await expect(
      authenticatedPage.getByRole('columnheader', { name: 'Business Unit' }),
    ).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Instrument' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Status' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Postings' })).toBeVisible()
  })

  test('renders Status filter select', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await expect(authenticatedPage.getByLabel('Status')).toBeVisible()
  })
})

test.describe('Booking log detail page', () => {
  test('renders error state for unknown booking log ID', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger/non-existent-booking-log-id')
    // Without a backend, the query fails and the error state renders
    await expect(
      authenticatedPage.getByText(/Failed to load booking log/),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('renders Ledger breadcrumb link on error state', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger/non-existent-booking-log-id')
    // Scope to main content to avoid matching the nav sidebar link
    const main = authenticatedPage.getByRole('main')
    await expect(
      main.getByRole('link', { name: 'Ledger' }),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('Ledger breadcrumb link navigates back to ledger list', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger/non-existent-booking-log-id')
    // Scope to main content to avoid matching the nav sidebar link
    const main = authenticatedPage.getByRole('main')
    await expect(
      main.getByRole('link', { name: 'Ledger' }),
    ).toBeVisible({ timeout: 10_000 })
    await main.getByRole('link', { name: 'Ledger' }).click()
    await expect(authenticatedPage.getByRole('heading', { name: 'Ledger' })).toBeVisible()
  })
})

/**
 * Full data tests — require live backend with seeded booking log records.
 * Un-skip when running against a full stack (task 45 CI workflow).
 */
test.describe('Ledger page with data (requires backend)', () => {
  test.skip(
    process.env.MERIDIAN_E2E_BACKEND !== '1',
    'Set MERIDIAN_E2E_BACKEND=1 to run data-dependent tests',
  )

  test('displays booking log rows in the table', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    const rows = authenticatedPage.locator('[data-testid="data-table-row"]')
    expect(await rows.count()).toBeGreaterThan(0)
  })

  test('navigates to booking log detail on row click', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(authenticatedPage).toHaveURL(/\/ledger\/[a-zA-Z0-9_-]+/)
    await expect(authenticatedPage.getByText('Ledger Postings')).toBeVisible({ timeout: 10_000 })
  })

  test('booking log detail shows Ledger Postings card title', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(authenticatedPage.getByText('Ledger Postings')).toBeVisible({ timeout: 10_000 })
  })

  test('booking log detail shows postings table with correct column headers', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(
      authenticatedPage.getByRole('columnheader', { name: 'Posting ID' }),
    ).toBeVisible({ timeout: 10_000 })
    await expect(
      authenticatedPage.getByRole('columnheader', { name: 'Direction' }),
    ).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Amount' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Account' })).toBeVisible()
  })

  test('booking log detail shows BalanceIndicator', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    const balanceIndicator = authenticatedPage.getByTestId('balance-indicator')
    await expect(balanceIndicator).toBeVisible({ timeout: 10_000 })
  })

  test('balance indicator shows debit and credit totals', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(authenticatedPage.getByTestId('debit-total')).toBeVisible({ timeout: 10_000 })
    await expect(authenticatedPage.getByTestId('credit-total')).toBeVisible()
  })

  test('booking log detail shows DirectionBadge with DEBIT or CREDIT', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/ledger')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(authenticatedPage.getByText('Ledger Postings')).toBeVisible({ timeout: 10_000 })
    const directionBadges = authenticatedPage.locator('[data-testid="direction-badge"]')
    const count = await directionBadges.count()
    expect(count).toBeGreaterThan(0)
    // Each posting has a DEBIT or CREDIT direction badge
    const texts = await directionBadges.allTextContents()
    const validDirections = texts.every(
      (t) => t.toUpperCase().includes('DEBIT') || t.toUpperCase().includes('CREDIT'),
    )
    expect(validDirections).toBe(true)
  })
})
