import { test, expect, navigateTo } from '../fixtures'
import { switchToTab } from '../helpers/parties'

/**
 * E2E tests for the party -> account -> transactions UI flow.
 *
 * Covers the cross-feature navigation path:
 *   Party list → Party detail (Accounts tab) → Account detail (Transactions tab)
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 * API calls fail gracefully when the backend is unavailable — tests verify
 * page structure and navigation, not live data.
 *
 * When no backend is available, the Accounts tab renders an empty table and
 * the account detail URL is unreachable (shows not-found). Tests handle both
 * states so the suite passes in smoke-test mode.
 *
 * Auth tokens are memory-only. All tests use navigateTo() instead of
 * page.goto() to preserve the in-memory auth state.
 */

// ---------------------------------------------------------------------------
// Party Accounts tab — table structure
// ---------------------------------------------------------------------------

test.describe('Party detail — Accounts tab structure', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    await page.waitForLoadState('networkidle')
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      await navigateTo(page, '/parties/00000000-0000-0000-0000-000000000000')
    } else {
      await page.locator('table tbody tr').first().click()
    }
    await expect(page.getByLabel('Breadcrumb').getByRole('link', { name: 'Parties' })).toBeVisible()
  })

  test('Accounts tab trigger is present in the 8-tab list', async ({ authenticatedPage: page }) => {
    await expect(page.getByRole('tab', { name: 'Accounts', exact: true })).toBeVisible()
  })

  test('Accounts tab activates on click', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Accounts')
    await expect(page.getByRole('tab', { name: 'Accounts', exact: true, selected: true })).toBeVisible()
  })

  test('Accounts tab renders a table', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Accounts')
    // DataTable always renders a <table> even in loading or empty state
    await expect(page.locator('table')).toBeVisible({ timeout: 10_000 })
  })

  test('Accounts tab table has expected column headers', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Accounts')
    // Wait for the table to be rendered
    await expect(page.locator('table')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByRole('columnheader', { name: 'Account ID' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'External Ref' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Status' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Instrument' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Created' })).toBeVisible()
  })
})

// ---------------------------------------------------------------------------
// Account detail page — structure without backend
// ---------------------------------------------------------------------------

test.describe('Account detail page — structure', () => {
  test('not-found state renders breadcrumb back to Accounts', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/accounts/00000000-0000-0000-0000-000000000000')
    // AccountNotFound renders both the breadcrumb link AND the not-found div simultaneously.
    // Use .first() to avoid strict-mode violation when both match.
    await expect(
      page.getByLabel('Breadcrumb').getByRole('link', { name: 'Accounts' })
        .or(page.getByTestId('account-not-found'))
        .first()
    ).toBeVisible({ timeout: 10_000 })
  })

  test('not-found state shows "Account not found" message', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/accounts/00000000-0000-0000-0000-000000000000')
    await expect(page.getByText('Account not found')).toBeVisible({ timeout: 10_000 })
  })

  test('back link on not-found page returns to Accounts list', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/accounts/00000000-0000-0000-0000-000000000000')
    const breadcrumb = page.getByLabel('Breadcrumb').getByRole('link', { name: 'Accounts' })
    await expect(breadcrumb).toBeVisible({ timeout: 10_000 })
    await breadcrumb.click()
    await expect(page.getByRole('heading', { name: 'Accounts' })).toBeVisible()
  })
})

// ---------------------------------------------------------------------------
// Account detail page — tabs (requires backend with live account data)
// ---------------------------------------------------------------------------

test.describe('Account detail page — tabs (requires backend)', () => {
  test.skip(
    process.env.MERIDIAN_E2E_BACKEND !== '1',
    'Set MERIDIAN_E2E_BACKEND=1 to run account detail tab tests against a live backend',
  )

  test.beforeEach(async ({ authenticatedPage: page }) => {
    // Navigate to the first available account from the accounts list
    await navigateTo(page, '/accounts')
    await expect(page.locator('table tbody tr').first()).toBeVisible({ timeout: 10_000 })
    await page.locator('table tbody tr').first().click()
    await expect(page).toHaveURL(/\/accounts\/[a-zA-Z0-9_-]+/)
  })

  test('renders Overview, Transactions, Liens, and Audit Trail tabs', async ({
    authenticatedPage: page,
  }) => {
    await expect(page.getByRole('tab', { name: 'Overview' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Transactions' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Liens' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Audit Trail' })).toBeVisible()
  })

  test('Overview tab is selected by default', async ({ authenticatedPage: page }) => {
    await expect(page.getByRole('tab', { name: 'Overview', selected: true })).toBeVisible()
  })

  test('Transactions tab renders table headers when clicked', async ({
    authenticatedPage: page,
  }) => {
    await page.getByRole('tab', { name: 'Transactions' }).click()
    await expect(page.getByRole('tab', { name: 'Transactions', selected: true })).toBeVisible()
    // Transactions table renders either data rows or the "No transactions" empty state
    await expect(
      page.getByRole('columnheader', { name: 'Direction' })
        .or(page.getByText('No transactions found'))
    ).toBeVisible({ timeout: 10_000 })
  })

  test('Liens tab renders when clicked', async ({ authenticatedPage: page }) => {
    await page.getByRole('tab', { name: 'Liens' }).click()
    await expect(page.getByRole('tab', { name: 'Liens', selected: true })).toBeVisible()
    // Liens renders either data rows or the "No active liens" empty state
    await expect(
      page.getByRole('columnheader', { name: 'Lien ID' })
        .or(page.getByText('No active liens'))
    ).toBeVisible({ timeout: 10_000 })
  })
})

// ---------------------------------------------------------------------------
// Party -> Account navigation flow (requires backend with seeded party data)
// ---------------------------------------------------------------------------

test.describe('Party to account navigation flow (requires backend)', () => {
  test.skip(
    process.env.MERIDIAN_E2E_BACKEND !== '1',
    'Set MERIDIAN_E2E_BACKEND=1 to run cross-feature navigation tests against a live backend',
  )

  test('party Accounts tab rows are clickable and navigate to account detail', async ({
    authenticatedPage: page,
  }) => {
    // Start from parties list
    await navigateTo(page, '/parties')
    await page.waitForLoadState('networkidle')
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      test.skip()
      return
    }

    // Navigate to first party detail
    await page.locator('table tbody tr').first().click()
    await expect(page).toHaveURL(/\/parties\/([a-zA-Z0-9-]+)/)

    // Open the Accounts tab
    await switchToTab(page, 'Accounts')
    await expect(page.getByRole('tab', { name: 'Accounts', exact: true, selected: true })).toBeVisible()

    // If this party has accounts, clicking a row navigates to the account detail page
    const accountRowCount = await page.locator('table tbody tr').count()
    if (accountRowCount === 0) {
      // No accounts for this party — verify empty state message
      await expect(
        page.getByText('No results').or(page.getByText('No data'))
      ).toBeVisible({ timeout: 10_000 })
      return
    }

    await page.locator('table tbody tr').first().click()
    await expect(page).toHaveURL(/\/accounts\/[a-zA-Z0-9_-]+/)

    // Verify breadcrumb back to Accounts list
    await expect(
      page.getByLabel('Breadcrumb').getByRole('link', { name: 'Accounts' })
    ).toBeVisible({ timeout: 10_000 })
  })

  test('account detail shows balance field', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/accounts')
    await page.waitForLoadState('networkidle')
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      test.skip()
      return
    }

    await page.locator('table tbody tr').first().click()
    await expect(page).toHaveURL(/\/accounts\/[a-zA-Z0-9_-]+/)

    // Available Balance field is always rendered in the summary card
    await expect(page.getByText('Available Balance')).toBeVisible({ timeout: 10_000 })
  })
})
