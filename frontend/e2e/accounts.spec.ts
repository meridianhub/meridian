import { test, expect, navigateTo } from './fixtures'

/**
 * Account lifecycle E2E tests.
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 *
 * Auth tokens are memory-only (not persisted to localStorage). All tests use
 * navigateTo() for client-side navigation to preserve the in-memory auth state.
 * page.goto() after authentication would trigger a full-page reload and lose the token.
 *
 * Navigation, page structure, and dialog open/close tests do NOT require the
 * Meridian backend — the accounts page renders with an empty/error state when
 * no backend is available, and the Create Account dialog is purely client-side.
 *
 * Tests that submit forms to create/deposit/withdraw are marked as requiring a
 * live backend (see task 45 for CI E2E workflow).
 *
 * Tests use `authenticatedPage` (tenant-user role) since accounts are
 * tenant-scoped resources.
 */

test.describe('Accounts page', () => {
  test('renders Accounts heading after navigation', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/accounts')
    await expect(authenticatedPage.getByRole('heading', { name: 'Accounts' })).toBeVisible()
  })

  test('renders Create Account button', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/accounts')
    await expect(
      authenticatedPage.getByRole('button', { name: 'Create Account' }),
    ).toBeVisible()
  })

  test('opens Create Account dialog on button click', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/accounts')
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    await expect(authenticatedPage.getByRole('dialog')).toBeVisible()
    await expect(
      authenticatedPage.getByRole('heading', { name: 'Create Account' }),
    ).toBeVisible()
  })

  test('Create Account dialog contains External Reference, Currency, and Party ID fields', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/accounts')
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog.getByLabel('External Reference')).toBeVisible()
    await expect(dialog.getByLabel('Currency')).toBeVisible()
    await expect(dialog.getByLabel('Party ID')).toBeVisible()
  })

  test('Create Account dialog closes on Cancel', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/accounts')
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    await expect(authenticatedPage.getByRole('dialog')).toBeVisible()
    await authenticatedPage.getByRole('button', { name: 'Cancel' }).click()
    await expect(authenticatedPage.getByRole('dialog')).not.toBeVisible()
  })

  test('Create Account dialog validates required fields', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/accounts')
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    // Submit without filling any fields
    await dialog.getByRole('button', { name: 'Create Account', exact: true }).click()
    await expect(dialog.getByText('External reference is required')).toBeVisible()
    await expect(dialog.getByText('Party ID is required')).toBeVisible()
  })

  test('Create Account dialog resets fields after cancel and reopen', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/accounts')
    // Open and fill the dialog
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    let dialog = authenticatedPage.getByRole('dialog')
    await dialog.getByLabel('External Reference').fill('GB82WEST12345698765432')
    await dialog.getByLabel('Party ID').fill('party-001')
    // Cancel
    await authenticatedPage.getByRole('button', { name: 'Cancel' }).click()
    // Reopen - fields should be reset
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog.getByLabel('External Reference')).toHaveValue('')
    await expect(dialog.getByLabel('Party ID')).toHaveValue('')
  })

  test('Currency select defaults to GBP', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/accounts')
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog.getByLabel('Currency')).toHaveValue('GBP')
  })
})

test.describe('Account detail page', () => {
  test('renders Account not found for unknown account ID', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/accounts/non-existent-account-id')
    // Without a backend, the query will fail → AccountNotFound is shown
    await expect(
      authenticatedPage.getByText('Account not found'),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('back link on not-found page navigates to Accounts list', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/accounts/non-existent-account-id')
    await expect(authenticatedPage.getByRole('link', { name: 'Back to Accounts' })).toBeVisible({
      timeout: 10_000,
    })
    await authenticatedPage.getByRole('link', { name: 'Back to Accounts' }).click()
    await expect(authenticatedPage.getByRole('heading', { name: 'Accounts' })).toBeVisible()
  })
})

/**
 * Full lifecycle tests — require live backend + seeded dev tenant data.
 * These are annotated with .skip so they pass in smoke-test mode (no backend).
 * Un-skip when running against a full stack (task 45 CI workflow).
 *
 * Party ID: 'dev-party-001' must exist in the dev tenant seed data.
 */
test.describe('Account lifecycle (requires backend)', () => {
  test.skip(
    process.env.MERIDIAN_E2E_BACKEND !== '1',
    'Set MERIDIAN_E2E_BACKEND=1 to run full lifecycle tests',
  )

  test('complete account lifecycle: create, deposit, withdraw, verify balance', async ({
    authenticatedPage,
  }) => {
    const testIban = `GB82WEST${Date.now().toString().slice(-14).padStart(14, '0')}`
    const currency = 'GBP'

    // Step 1: Navigate to Accounts
    await navigateTo(authenticatedPage, '/accounts')
    await expect(authenticatedPage.getByRole('heading', { name: 'Accounts' })).toBeVisible()

    // Step 2: Open Create Account dialog
    await authenticatedPage.getByRole('button', { name: 'Create Account' }).click()
    await expect(authenticatedPage.getByRole('dialog')).toBeVisible()

    // Step 3: Fill and submit form (scope to dialog to avoid collision with DataTable filter)
    const createDialog = authenticatedPage.getByRole('dialog')
    await createDialog.getByLabel('External Reference').fill(testIban)
    await createDialog.getByLabel('Currency').selectOption(currency)
    await createDialog.getByLabel('Party ID').fill('dev-party-001')
    await createDialog.getByRole('button', { name: 'Create Account', exact: true }).click()

    // Step 4: After creation, navigates to account detail page
    await expect(authenticatedPage.getByText(testIban)).toBeVisible({ timeout: 10_000 })

    // Capture account detail URL (navigated by onCreated callback)
    const url = authenticatedPage.url()
    expect(url).toMatch(/\/accounts\/[a-zA-Z0-9_-]+$/)

    // Step 5: Initial balance is zero
    await expect(authenticatedPage.getByText('0.00')).toBeVisible({ timeout: 5_000 })

    // Step 6: Deposit funds
    await authenticatedPage.getByRole('button', { name: 'Deposit' }).click()
    const depositDialog = authenticatedPage.getByRole('dialog')
    await expect(depositDialog).toBeVisible()
    await expect(depositDialog.getByRole('heading', { name: 'Deposit Funds' })).toBeVisible()
    await depositDialog.getByLabel('Amount').fill('100.00')
    await depositDialog.getByRole('button', { name: 'Deposit', exact: true }).click()
    await expect(depositDialog).not.toBeVisible({ timeout: 5_000 })
    await expect(authenticatedPage.getByText(`${currency} 100.00`)).toBeVisible({ timeout: 5_000 })

    // Step 7: Withdraw funds (two-phase: initiate → confirm)
    await authenticatedPage.getByRole('button', { name: 'Withdraw' }).click()
    const withdrawDialog = authenticatedPage.getByRole('dialog')
    await expect(withdrawDialog).toBeVisible()
    await expect(withdrawDialog.getByRole('heading', { name: 'Withdraw Funds' })).toBeVisible()
    await withdrawDialog.getByLabel('Amount').fill('25.00')
    // Initiate step
    await withdrawDialog.getByRole('button', { name: 'Initiate', exact: true }).click()
    // Confirm step — dialog shows confirmation details
    await expect(withdrawDialog.getByText(/confirm withdrawal/i)).toBeVisible()
    await withdrawDialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(withdrawDialog).not.toBeVisible({ timeout: 5_000 })

    // Step 8: Final balance = 100.00 - 25.00 = 75.00
    await expect(authenticatedPage.getByText(`${currency} 75.00`)).toBeVisible({ timeout: 5_000 })
  })

  test('deposit dialog shows error for empty amount', async ({ authenticatedPage }) => {
    // Navigate to an existing active account via accounts list
    await navigateTo(authenticatedPage, '/accounts')
    // Click the first data row to navigate to the account detail page
    // (nth(0) is the header row, nth(1) is the first data row)
    await authenticatedPage.getByRole('row').nth(1).click()
    // Deposit button exists on the account detail page
    await authenticatedPage.getByRole('button', { name: 'Deposit' }).click()
    const depositDialog = authenticatedPage.getByRole('dialog')
    await expect(depositDialog).toBeVisible()
    // Submit with empty amount
    await depositDialog.getByRole('button', { name: 'Deposit', exact: true }).click()
    await expect(authenticatedPage.getByText('Amount is required')).toBeVisible()
  })
})
