import { test, expect, navigateTo } from './fixtures'

/**
 * Reconciliation page E2E tests.
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 *
 * Auth tokens are memory-only (not persisted). All tests use navigateTo() for
 * client-side navigation to preserve in-memory auth state.
 *
 * Navigation and page structure tests do NOT require the Meridian backend —
 * the reconciliation page renders with an empty/error state when no backend is
 * available.
 *
 * Tests that require real reconciliation run data (row click, detail tabs,
 * disputes UI, balance assertions) are guarded by MERIDIAN_E2E_BACKEND=1.
 *
 * Tests use `authenticatedPage` (tenant-user role) since reconciliation data
 * is tenant-scoped.
 */

test.describe('Reconciliation page', () => {
  test('renders Reconciliation heading after navigation', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await expect(
      authenticatedPage.getByRole('heading', { name: 'Reconciliation' }),
    ).toBeVisible()
  })

  test('renders reconciliation page subtitle', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await expect(
      authenticatedPage.getByText('Settlement runs, variance detection, and dispute resolution.'),
    ).toBeVisible()
  })

  test('renders reconciliation table column headers', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Run ID' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Account' })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Scope' })).toBeVisible()
    await expect(
      authenticatedPage.getByRole('columnheader', { name: 'Settlement Type' }),
    ).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: 'Status' })).toBeVisible()
    await expect(
      authenticatedPage.getByRole('columnheader', { name: 'Variances' }),
    ).toBeVisible()
  })

  test('renders Status filter select', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await expect(authenticatedPage.getByLabel('Status')).toBeVisible()
  })

  test('renders Account ID filter input', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await expect(authenticatedPage.getByPlaceholder('Account ID')).toBeVisible()
  })
})

test.describe('Reconciliation detail page', () => {
  test('renders error state for unknown reconciliation run ID', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation/non-existent-run-id')
    await expect(
      authenticatedPage.getByText(/Failed to load reconciliation run/),
    ).toBeVisible({ timeout: 10_000 })
  })
})

/**
 * Full data tests — require live backend with seeded reconciliation run records.
 * Un-skip when running against a full stack (task 45 CI workflow).
 */
test.describe('Reconciliation page with data (requires backend)', () => {
  test.skip(
    process.env.MERIDIAN_E2E_BACKEND !== '1',
    'Set MERIDIAN_E2E_BACKEND=1 to run data-dependent tests',
  )

  test('displays reconciliation run rows or empty state', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    // Either data rows or empty state (no rows yet in seed data)
    const hasRows =
      (await authenticatedPage.locator('[data-testid="data-table-row"]').count()) > 0
    const hasEmptyState = await authenticatedPage
      .getByText(/No data/i)
      .isVisible()
      .catch(() => false)
    expect(hasRows || hasEmptyState).toBe(true)
  })

  test('navigates to reconciliation detail on row click', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(authenticatedPage).toHaveURL(/\/reconciliation\/[a-zA-Z0-9_-]+/)
  })

  test('reconciliation detail shows Variances tab', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(
      authenticatedPage.getByRole('tab', { name: 'Variances' }),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('reconciliation detail shows Disputes tab', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(
      authenticatedPage.getByRole('tab', { name: 'Disputes' }),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('reconciliation detail shows Balance Assertions tab', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await expect(
      authenticatedPage.getByRole('tab', { name: 'Balance Assertions' }),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('Disputes tab shows status filter buttons', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await authenticatedPage.getByRole('tab', { name: 'Disputes' }).click()
    const filterGroup = authenticatedPage.getByRole('group', { name: 'Dispute status filter' })
    await expect(filterGroup).toBeVisible({ timeout: 10_000 })
    await expect(filterGroup.getByRole('button', { name: 'ALL' })).toBeVisible()
    await expect(filterGroup.getByRole('button', { name: 'OPEN' })).toBeVisible()
    await expect(filterGroup.getByRole('button', { name: 'RESOLVED' })).toBeVisible()
    await expect(filterGroup.getByRole('button', { name: 'REJECTED' })).toBeVisible()
  })

  test('Disputes tab shows disputes or empty state', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await authenticatedPage.getByRole('tab', { name: 'Disputes' }).click()
    // Wait for disputes to load
    await authenticatedPage.waitForSelector('[data-testid="dispute-skeleton"]', {
      state: 'hidden',
      timeout: 10_000,
    }).catch(() => {})
    const hasCards =
      (await authenticatedPage.locator('[data-testid="dispute-card"]').count()) > 0
    const hasEmpty = await authenticatedPage
      .getByTestId('disputes-empty')
      .isVisible()
      .catch(() => false)
    expect(hasCards || hasEmpty).toBe(true)
  })

  test('Balance Assertions tab shows assertion form', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await authenticatedPage.getByRole('tab', { name: 'Balance Assertions' }).click()
    // Wait for assertions to load
    await authenticatedPage.waitForSelector('[data-testid="assertion-skeleton"]', {
      state: 'hidden',
      timeout: 10_000,
    }).catch(() => {})
    await expect(
      authenticatedPage.getByTestId('assertion-form'),
    ).toBeVisible({ timeout: 10_000 })
    await expect(authenticatedPage.getByLabel('Name')).toBeVisible()
    await expect(authenticatedPage.getByTestId('save-assertion-btn')).toBeVisible()
  })

  test('Balance Assertions form validates empty submission', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reconciliation')
    await authenticatedPage.waitForSelector('[data-testid="data-table-row"]', {
      state: 'visible',
      timeout: 10_000,
    })
    await authenticatedPage.locator('[data-testid="data-table-row"]').first().click()
    await authenticatedPage.getByRole('tab', { name: 'Balance Assertions' }).click()
    await authenticatedPage.waitForSelector('[data-testid="assertion-skeleton"]', {
      state: 'hidden',
      timeout: 10_000,
    }).catch(() => {})
    await authenticatedPage.getByTestId('save-assertion-btn').click()
    await expect(
      authenticatedPage.getByTestId('assertion-error'),
    ).toBeVisible({ timeout: 5_000 })
    await expect(
      authenticatedPage.getByText('Name and expression are required.'),
    ).toBeVisible()
  })
})
