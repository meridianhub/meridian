import { test, expect, navigateTo } from './fixtures'

/**
 * Payment flow E2E tests.
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 *
 * Auth tokens are memory-only (not persisted to localStorage). All tests use
 * navigateTo() for client-side navigation to preserve the in-memory auth state.
 * page.goto() after authentication would trigger a full-page reload and lose the token.
 *
 * Navigation, page structure, and dialog open/close tests do NOT require the
 * Meridian backend — the payments page renders with an empty/error state when
 * no backend is available, and the Initiate Payment dialog is purely client-side.
 *
 * Tests that submit forms to create payments are marked as requiring a
 * live backend (see task 45 for CI E2E workflow).
 *
 * Tests use `authenticatedPage` (tenant-user role) since payments are
 * tenant-scoped resources.
 */

test.describe('Payments page', () => {
  test('renders Payments heading after navigation', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await expect(authenticatedPage.getByRole('heading', { name: 'Payments' })).toBeVisible()
  })

  test('renders payment table column headers', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await expect(authenticatedPage.getByRole('columnheader', { name: /payment id/i })).toBeVisible()
    await expect(
      authenticatedPage.getByRole('columnheader', { name: /debtor account/i }),
    ).toBeVisible()
    await expect(
      authenticatedPage.getByRole('columnheader', { name: /creditor iban/i }),
    ).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: /amount/i })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: /status/i })).toBeVisible()
    await expect(authenticatedPage.getByRole('columnheader', { name: /created/i })).toBeVisible()
  })

  test('renders status filter select', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await expect(authenticatedPage.getByRole('combobox', { name: /status/i })).toBeVisible()
  })

  test('renders payments table element', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await expect(authenticatedPage.getByRole('table')).toBeVisible()
  })

  test('payments list page does not show Initiate Payment button', async ({
    authenticatedPage,
  }) => {
    // The Initiate Payment dialog is only accessible from the payment detail page
    // (via the "New Payment" button). The list page does not have this button.
    await navigateTo(authenticatedPage, '/payments')
    await expect(authenticatedPage.getByRole('heading', { name: 'Payments' })).toBeVisible()
    await expect(
      authenticatedPage.getByRole('button', { name: /initiate payment/i }),
    ).not.toBeVisible()
  })
})

test.describe('Payment detail page', () => {
  test('renders error state for unknown payment ID', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments/non-existent-payment-id')
    await expect(
      authenticatedPage.getByTestId('payment-detail-error'),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('renders error message for unknown payment ID', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments/non-existent-payment-id')
    await expect(
      authenticatedPage.getByText('Failed to load payment order details.'),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('back link on error page navigates to Payments list', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments/non-existent-payment-id')
    await expect(
      authenticatedPage.getByRole('link', { name: 'Payments' }),
    ).toBeVisible({ timeout: 10_000 })
    await authenticatedPage.getByRole('link', { name: 'Payments' }).click()
    await expect(authenticatedPage.getByRole('heading', { name: 'Payments' })).toBeVisible()
  })
})

test.describe('Initiate Payment dialog (no backend required)', () => {
  /**
   * The InitiatePaymentDialog is embedded in PaymentDetailPage and opened via the
   * "New Payment" button. Without a backend the detail page shows an error state, so
   * the "New Payment" button is unreachable in smoke-test mode.
   *
   * These tests instead verify dialog form behaviour by directly testing the
   * payments list page (no dialog) and the detail page error state, which are
   * fully client-side.
   *
   * Form validation and dialog interaction tests are exercised in the backend-required
   * describe block below, which is skipped in smoke-test mode.
   */

  test('payment detail page error state contains back link', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments/test-id')
    const errorElement = authenticatedPage.getByTestId('payment-detail-error')
    await expect(errorElement).toBeVisible({ timeout: 10_000 })
    await expect(errorElement.getByRole('link', { name: 'Payments' })).toBeVisible()
  })
})

/**
 * Full lifecycle tests — require live backend + seeded dev tenant data.
 * These are annotated with .skip so they pass in smoke-test mode (no backend).
 * Un-skip when running against a full stack (task 45 CI workflow).
 *
 * Party ID: 'dev-party-001' and account ID: 'dev-account-001' must exist in
 * the dev tenant seed data.
 */
test.describe('Payment lifecycle (requires backend)', () => {
  test.skip(
    process.env.MERIDIAN_E2E_BACKEND !== '1',
    'Set MERIDIAN_E2E_BACKEND=1 to run full lifecycle tests',
  )

  test('payment detail page renders tabs: Overview, Saga Steps, Audit Trail', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/payments')
    // Navigate to first payment row to get to detail page
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await expect(authenticatedPage.getByRole('tab', { name: 'Overview' })).toBeVisible()
    await expect(authenticatedPage.getByRole('tab', { name: 'Saga Steps' })).toBeVisible()
    await expect(authenticatedPage.getByRole('tab', { name: 'Audit Trail' })).toBeVisible()
  })

  test('payment detail Saga Steps tab renders Saga Progression section', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('tab', { name: 'Saga Steps' }).click()
    await expect(authenticatedPage.getByText('Saga Progression')).toBeVisible()
  })

  test('payment detail page shows New Payment button', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)
    await expect(authenticatedPage.getByRole('button', { name: 'New Payment' })).toBeVisible()
  })

  test('opens Initiate Payment dialog from New Payment button', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByRole('heading', { name: 'Initiate Payment' })).toBeVisible()
  })

  test('Initiate Payment dialog contains required form fields', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await expect(dialog.getByLabel('Debtor Account')).toBeVisible()
    await expect(dialog.getByLabel('Creditor IBAN')).toBeVisible()
    await expect(dialog.getByLabel('Amount')).toBeVisible()
    await expect(dialog.getByLabel('Currency')).toBeVisible()
  })

  test('Initiate Payment dialog Currency defaults to GBP', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByLabel('Currency')).toHaveValue('GBP')
  })

  test('Initiate Payment dialog validates required fields', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()

    // Submit without filling any fields
    await dialog.getByRole('button', { name: 'Initiate Payment' }).click()
    await expect(dialog.getByText('Debtor account is required')).toBeVisible()
    await expect(dialog.getByText('IBAN is required')).toBeVisible()
    await expect(dialog.getByText('Amount is required')).toBeVisible()
  })

  test('Initiate Payment dialog validates IBAN format', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await dialog.getByLabel('Debtor Account').fill('dev-account-001')
    await dialog.getByLabel('Creditor IBAN').fill('not-a-valid-iban')
    await dialog.getByLabel('Amount').fill('10.00')
    await dialog.getByRole('button', { name: 'Initiate Payment' }).click()
    await expect(dialog.getByText('Invalid IBAN format')).toBeVisible()
  })

  test('Initiate Payment dialog closes on Cancel', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await authenticatedPage.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).not.toBeVisible()
  })

  test('Initiate Payment dialog resets fields after cancel and reopen', async ({
    authenticatedPage,
  }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    // Open dialog and fill fields
    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    let dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Debtor Account').fill('dev-account-001')
    await dialog.getByLabel('Creditor IBAN').fill('GB29NWBK60161331926819')
    await dialog.getByLabel('Amount').fill('10.00')

    // Cancel and reopen
    await authenticatedPage.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).not.toBeVisible()
    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()

    // Fields should be reset
    await expect(dialog.getByLabel('Debtor Account')).toHaveValue('')
    await expect(dialog.getByLabel('Creditor IBAN')).toHaveValue('')
    await expect(dialog.getByLabel('Amount')).toHaveValue('')
  })

  test('IEEE-754 precision: initiate payment with amount 0.29 and verify display', async ({
    authenticatedPage,
  }) => {
    // Navigate to payments list and then to detail
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    // Open Initiate Payment dialog
    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()

    // Fill with IEEE-754 edge case amount.
    // 0.29 in IEEE-754 binary64 is 0.28999999...
    // Without string-based BigInt parsing: Math.round(0.29 * 100) = 28 → GBP 0.28 (wrong)
    // With amountToBigInt string parsing: "0.29" → 29n → GBP 0.29 (correct)
    await dialog.getByLabel('Debtor Account').fill('dev-account-001')
    await dialog.getByLabel('Creditor IBAN').fill('GB29NWBK60161331926819')
    await dialog.getByLabel('Amount').fill('0.29')
    await dialog.getByRole('button', { name: 'Initiate Payment' }).click()

    // Dialog closes and navigates to the new payment's detail page
    await expect(dialog).not.toBeVisible({ timeout: 10_000 })
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    // Verify amount displays as GBP 0.29, NOT GBP 0.28
    await expect(authenticatedPage.getByText('GBP 0.29')).toBeVisible({ timeout: 10_000 })
    await expect(authenticatedPage.getByText('GBP 0.28')).not.toBeVisible()
  })

  test('IEEE-754 precision: additional edge cases (0.01, 0.10, 99.99, 1000.00)', async ({
    authenticatedPage,
  }) => {
    const edgeCases = [
      { amount: '0.01', expected: 'GBP 0.01' },
      { amount: '0.10', expected: 'GBP 0.10' },
      { amount: '99.99', expected: 'GBP 99.99' },
      { amount: '1000.00', expected: 'GBP 1000.00' },
    ]

    for (const { amount, expected } of edgeCases) {
      await navigateTo(authenticatedPage, '/payments')
      await authenticatedPage.getByRole('row').nth(1).click()
      await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

      await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
      const dialog = authenticatedPage.getByRole('dialog')
      await expect(dialog).toBeVisible()

      await dialog.getByLabel('Debtor Account').fill('dev-account-001')
      await dialog.getByLabel('Creditor IBAN').fill('GB29NWBK60161331926819')
      await dialog.getByLabel('Amount').fill(amount)
      await dialog.getByRole('button', { name: 'Initiate Payment' }).click()

      await expect(dialog).not.toBeVisible({ timeout: 10_000 })
      await expect(authenticatedPage.getByText(expected)).toBeVisible({ timeout: 10_000 })
    }
  })

  test('payment status shows a valid saga status after creation', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Debtor Account').fill('dev-account-001')
    await dialog.getByLabel('Creditor IBAN').fill('GB29NWBK60161331926819')
    await dialog.getByLabel('Amount').fill('10.00')
    await dialog.getByRole('button', { name: 'Initiate Payment' }).click()
    await expect(dialog).not.toBeVisible({ timeout: 10_000 })

    // Status should be one of the valid saga workflow states
    const statusPattern = /INITIATED|RESERVED|EXECUTING|COMPLETED|FAILED/
    await expect(authenticatedPage.getByText(statusPattern).first()).toBeVisible({
      timeout: 10_000,
    })
  })

  test('payment appears in list after creation', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/payments')
    await expect(authenticatedPage.getByRole('heading', { name: 'Payments' })).toBeVisible()
    const tableBody = authenticatedPage.getByRole('table').locator('tbody')
    const initialCount = await tableBody.locator('tr').count()

    // Navigate to detail to open the New Payment dialog
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Debtor Account').fill('dev-account-001')
    await dialog.getByLabel('Creditor IBAN').fill('GB29NWBK60161331926819')
    await dialog.getByLabel('Amount').fill('5.00')
    await dialog.getByRole('button', { name: 'Initiate Payment' }).click()
    await expect(dialog).not.toBeVisible({ timeout: 10_000 })

    // Navigate back to payments list
    await authenticatedPage.getByRole('link', { name: 'Payments' }).click()
    await expect(authenticatedPage.getByRole('heading', { name: 'Payments' })).toBeVisible()

    // List should have one more payment
    const newCount = await tableBody.locator('tr').count()
    expect(newCount).toBe(initialCount + 1)
  })

  test('ledger shows booking entries after payment creation', async ({ authenticatedPage }) => {
    // Initiate a payment
    await navigateTo(authenticatedPage, '/payments')
    await authenticatedPage.getByRole('row').nth(1).click()
    await expect(authenticatedPage).toHaveURL(/\/payments\/[a-zA-Z0-9_-]+$/)

    await authenticatedPage.getByRole('button', { name: 'New Payment' }).click()
    const dialog = authenticatedPage.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Debtor Account').fill('dev-account-001')
    await dialog.getByLabel('Creditor IBAN').fill('GB29NWBK60161331926819')
    await dialog.getByLabel('Amount').fill('15.00')
    await dialog.getByRole('button', { name: 'Initiate Payment' }).click()
    await expect(dialog).not.toBeVisible({ timeout: 10_000 })

    // Navigate to ledger
    await navigateTo(authenticatedPage, '/ledger')
    await expect(authenticatedPage.getByRole('heading', { name: 'Ledger' })).toBeVisible()

    // Booking logs table should have entries
    const bookingRows = authenticatedPage.getByRole('table').locator('tbody tr')
    await expect(bookingRows.first()).toBeVisible({ timeout: 10_000 })

    // Click booking log to navigate to detail
    await bookingRows.first().click()
    await expect(authenticatedPage).toHaveURL(/\/ledger\/[a-zA-Z0-9_-]+$/)
  })
})
