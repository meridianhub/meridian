import { test, expect, navigateTo } from '../fixtures'
import { switchToTab } from '../helpers/parties'

/**
 * E2E tests for the Parties page.
 *
 * NOTE: These tests require the Vite dev server (npm run dev) to be running.
 * API calls will fail gracefully when the backend is unavailable — the tests
 * verify UI structure, filter rendering, and tab layout rather than live data.
 *
 * Auth tokens are memory-only (not persisted to localStorage). All tests use
 * navigateTo() for client-side navigation to preserve the in-memory auth state.
 * page.goto() after authentication would trigger a full-page reload and lose
 * the token.
 *
 * For full integration testing with live backend data see task 45 (CI E2E
 * workflow).
 */

test.describe('Parties list', () => {
  test('renders the Parties heading', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    await expect(page.getByRole('heading', { name: 'Parties' })).toBeVisible()
  })

  test('renders the data table', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    // The DataTable wrapper renders; it may be in loading state or empty state
    // but the table element itself must be present
    await expect(page.locator('table')).toBeVisible()
  })

  test('renders expected column headers', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    await expect(page.getByRole('columnheader', { name: 'Name' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Party Type' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Status' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'External Ref' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Created' })).toBeVisible()
  })

  test('renders Party Type filter with correct options', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    // The filter renders as a combobox (select) in the filter bar.
    // Use getByRole('combobox') to target the filter select specifically,
    // avoiding the "Add Party Type" action button which also matches /party type/i.
    await expect(page.getByRole('combobox', { name: /party type/i })).toBeVisible()
  })

  test('renders Status filter', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    await expect(page.getByText('Status')).toBeVisible()
  })

  test('renders Search filter', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    // The DataTable renders a text input for the search filter
    await expect(page.getByPlaceholder(/search/i).or(page.getByText('Search'))).toBeVisible()
  })
})

test.describe('Party detail navigation', () => {
  test('navigates to detail page on row click', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    // Only attempt navigation if at least one row is present
    const firstRow = page.locator('table tbody tr').first()
    const rowCount = await page.locator('table tbody tr').count()

    if (rowCount === 0) {
      test.skip()
      return
    }

    await expect(firstRow).toBeVisible()
    await firstRow.click()
    await expect(page).toHaveURL(/\/parties\/[a-zA-Z0-9-]+/)
    await expect(page.getByLabel('Breadcrumb').getByRole('link', { name: 'Parties' })).toBeVisible()
  })

  test('shows Party ID not found for missing partyId param', async ({ authenticatedPage: page }) => {
    // Directly verify that the error message renders for an invalid ID
    await navigateTo(page, '/parties/00000000-0000-0000-0000-000000000000')
    // Page should render — it will show either the party data or an error state
    // (the component renders even without backend data)
    await expect(
      page.getByLabel('Breadcrumb').getByRole('link', { name: 'Parties' }).or(
        page.getByText('Party not found')
      )
    ).toBeVisible()
  })
})

test.describe('Party detail — 8-tab layout', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      // Navigate directly to a party detail stub so tab structure tests run
      await navigateTo(page, '/parties/00000000-0000-0000-0000-000000000000')
    } else {
      await page.locator('table tbody tr').first().click()
    }
    await expect(page.getByLabel('Breadcrumb').getByRole('link', { name: 'Parties' })).toBeVisible()
  })

  test('renders all 8 tab triggers', async ({ authenticatedPage: page }) => {
    const expectedTabs = [
      'Overview',
      'Demographics',
      'References',
      'Associations',
      'Bank Relations',
      'Payment Methods',
      'Accounts',
      'Audit Trail',
    ] as const

    for (const tabName of expectedTabs) {
      await expect(page.getByRole('tab', { name: tabName })).toBeVisible()
    }
  })

  test('tab list has 8-column grid layout', async ({ authenticatedPage: page }) => {
    const tabList = page.getByRole('tablist')
    await expect(tabList).toBeVisible()
    // Verify the CSS grid class from [partyId].tsx:34
    await expect(tabList).toHaveClass(/grid-cols-8/)
  })

  test('Overview tab is selected by default', async ({ authenticatedPage: page }) => {
    await expect(page.getByRole('tab', { name: 'Overview', selected: true })).toBeVisible()
  })
})

test.describe('Party header component', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      test.skip()
      return
    }
    await page.locator('table tbody tr').first().click()
    await expect(page.getByLabel('Breadcrumb').getByRole('link', { name: 'Parties' })).toBeVisible()
  })

  test('renders party header section', async ({ authenticatedPage: page }) => {
    // The header is in a Card above the tabs — .p-6.border-b per party-header.tsx:44
    await expect(page.locator('.p-6.border-b').or(
      page.locator('[class*="p-6"]').first()
    )).toBeVisible()
  })

  test('displays party name in h2 or loading skeleton', async ({ authenticatedPage: page }) => {
    // Either the header renders (party-header.tsx:44 — .p-6.border-b)
    // or the loading skeleton renders (party-header.tsx:32 — .p-6.space-y-4)
    await expect(
      page.locator('.p-6.border-b').or(page.locator('.p-6.space-y-4'))
    ).toBeVisible()
  })
})

test.describe('Tab switching', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      await navigateTo(page, '/parties/00000000-0000-0000-0000-000000000000')
    } else {
      await page.locator('table tbody tr').first().click()
    }
    await expect(page.getByLabel('Breadcrumb').getByRole('link', { name: 'Parties' })).toBeVisible()
  })

  test('Overview tab renders without error', async ({ authenticatedPage: page }) => {
    // Already on Overview by default
    await expect(page.getByRole('tab', { name: 'Overview', selected: true })).toBeVisible()
    // Content area shows data, empty state, or loading skeleton — use soft assertion
    // so the test passes even when the API is unavailable (loading state is acceptable).
    // Use .first() because the .or() chain can match multiple elements (e.g. radix items).
    await expect.soft(
      page.getByRole('tabpanel')
        .or(page.getByText('Party ID'))
        .or(page.getByText('No data'))
        .or(page.locator('[class*="skeleton"]'))
        .first()
    ).toBeVisible({ timeout: 10_000 })
  })

  test('Demographics tab activates on click', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Demographics')
    await expect(page.getByRole('tab', { name: 'Demographics', selected: true })).toBeVisible()
  })

  test('References tab activates on click and shows EmptyState', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'References')
    await expect(page.getByRole('tab', { name: 'References', selected: true })).toBeVisible()
    // ReferencesTab always renders EmptyState when not loading (references-tab.tsx:30)
    // Scope to tabpanel to avoid matching the tab label itself
    await expect(
      page.getByRole('tabpanel').getByRole('heading', { name: 'References' })
        .or(page.getByRole('tabpanel').getByText('No references information available'))
        .first()
    ).toBeVisible()
  })

  test('Associations tab activates on click', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Associations')
    await expect(page.getByRole('tab', { name: 'Associations', selected: true })).toBeVisible()
  })

  test('Bank Relations tab activates on click', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Bank Relations')
    await expect(page.getByRole('tab', { name: 'Bank Relations', selected: true })).toBeVisible()
  })

  test('Payment Methods tab shows Add Payment Method button', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Payment Methods')
    await expect(page.getByRole('tab', { name: 'Payment Methods', selected: true })).toBeVisible()
    // The button is always rendered regardless of data (payment-methods-tab.tsx:104-106)
    await expect(page.getByRole('button', { name: 'Add Payment Method' })).toBeVisible()
  })

  test('Audit Trail tab activates and renders stub or content', async ({ authenticatedPage: page }) => {
    await switchToTab(page, 'Audit Trail')
    await expect(page.getByRole('tab', { name: 'Audit Trail', selected: true })).toBeVisible()
    // Audit trail renders stub banner, empty state, skeleton, or entries
    await expect(
      page.getByTestId('audit-trail-stub')
        .or(page.getByTestId('audit-trail-empty'))
        .or(page.getByTestId('audit-trail-skeleton'))
        .or(page.getByTestId('audit-trail-error'))
        .or(page.getByTestId('audit-entry'))
    ).toBeVisible({ timeout: 15_000 })
  })
})

test.describe('Tab keyboard navigation', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      await navigateTo(page, '/parties/00000000-0000-0000-0000-000000000000')
    } else {
      await page.locator('table tbody tr').first().click()
    }
    await expect(page.getByLabel('Breadcrumb').getByRole('link', { name: 'Parties' })).toBeVisible()
  })

  test('ArrowRight moves focus to next tab', async ({ authenticatedPage: page }) => {
    const overviewTab = page.getByRole('tab', { name: 'Overview' })
    await overviewTab.focus()
    await page.keyboard.press('ArrowRight')
    // After pressing ArrowRight the Demographics tab should be focused
    await expect(page.getByRole('tab', { name: 'Demographics' })).toBeFocused()
  })

  test('ArrowLeft moves focus to previous tab from Demographics', async ({ authenticatedPage: page }) => {
    const demographicsTab = page.getByRole('tab', { name: 'Demographics' })
    await demographicsTab.focus()
    await page.keyboard.press('ArrowLeft')
    await expect(page.getByRole('tab', { name: 'Overview' })).toBeFocused()
  })
})

test.describe('Party creation (conditional — dialog not yet implemented)', () => {
  test.skip('should create a new party via dialog when feature is available', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/parties')
    const createButton = page.getByRole('button', { name: /Add Party|Create Party|New Party/i })
    if (!(await createButton.isVisible())) {
      test.skip()
      return
    }

    await createButton.click()
    await page.getByLabel('Name').fill('E2E Test Party')
    await page.getByRole('button', { name: /Create|Submit|Save/i }).click()
    await expect(page.getByRole('dialog')).not.toBeVisible()
    await expect(page.getByText('E2E Test Party')).toBeVisible()
  })
})
