import { test, expect, navigateTo } from './fixtures'

/**
 * Economy feature E2E tests.
 *
 * Tests run against the Vite dev server only — no backend required.
 * API calls fail gracefully and pages render with loading/empty states.
 *
 * Auth tokens are memory-only (not persisted). All tests use navigateTo()
 * for client-side navigation to preserve the in-memory auth state.
 *
 * Economy pages are lazy-loaded behind a FeatureGuard (feature: "economy").
 * All features are enabled by default, so the guard passes without config.
 */

test.describe('Economy Overview', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/economy')
    // Wait for lazy-loaded page to resolve from Suspense
    await page.waitForSelector('[data-testid="overview-loading"], [data-testid="overview-empty"], [data-testid="overview-error"], h1', { timeout: 15_000 })
  })

  test('renders the Economy page without error', async ({ authenticatedPage: page }) => {
    // Should not show error boundary
    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
  })

  test('shows breadcrumb with Economy label', async ({ authenticatedPage: page }) => {
    await expect(page.getByText('Economy', { exact: true }).first()).toBeVisible()
  })

  test('renders empty state or overview content', async ({ authenticatedPage: page }) => {
    // The page can resolve to empty state (NotFound), error state, or rendered content.
    // All are valid outcomes — the page loaded without crashing.
    const emptyState = page.getByTestId('overview-empty')
    const errorState = page.getByTestId('overview-error')
    const loadingState = page.getByTestId('overview-loading')

    // Wait for loading to complete
    await expect(loadingState).toHaveCount(0, { timeout: 15_000 })

    // One of empty, error, or content (h1 heading) should be visible
    const isEmpty = await emptyState.isVisible().catch(() => false)
    const isError = await errorState.isVisible().catch(() => false)
    const hasContent = await page.getByRole('heading', { level: 1 }).isVisible().catch(() => false)

    expect(isEmpty || isError || hasContent).toBe(true)
  })

  test('empty state has Configure Economy button', async ({ authenticatedPage: page }) => {
    const isVisible = await page.getByTestId('overview-empty').isVisible().catch(() => false)
    test.skip(!isVisible, 'Requires overview-empty state (no backend returns NotFound)')

    await expect(page.getByRole('button', { name: 'Configure Economy' })).toBeVisible()
  })

  test('Configure Economy button navigates to /economy/edit', async ({ authenticatedPage: page }) => {
    const isVisible = await page.getByTestId('overview-empty').isVisible().catch(() => false)
    test.skip(!isVisible, 'Requires overview-empty state to validate Configure Economy CTA')

    await page.getByRole('button', { name: 'Configure Economy' }).click()
    await expect(page).toHaveURL('/economy/edit')
  })

  test('error state has Retry button', async ({ authenticatedPage: page }) => {
    const isVisible = await page.getByTestId('overview-error').isVisible().catch(() => false)
    test.skip(!isVisible, 'Requires overview-error state to validate Retry button')

    await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
  })
})

test.describe('Economy Explorer', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/economy/explore')
    await page.waitForSelector('[data-testid="explorer-loading"], [data-testid="explorer-empty"], [data-testid="explorer-error"], h1', { timeout: 15_000 })
  })

  test('renders the Economy Explorer page without error', async ({ authenticatedPage: page }) => {
    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
  })

  test('shows breadcrumbs with Economy and Explore', async ({ authenticatedPage: page }) => {
    // Breadcrumbs: Economy > Explore
    await expect(page.getByText('Economy', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('Explore', { exact: true }).first()).toBeVisible()
  })

  test('renders empty state or explorer content', async ({ authenticatedPage: page }) => {
    const emptyState = page.getByTestId('explorer-empty')
    const errorState = page.getByTestId('explorer-error')
    const loadingState = page.getByTestId('explorer-loading')

    await expect(loadingState).toHaveCount(0, { timeout: 15_000 })

    // One of empty, error, or content (h1 heading) should be visible
    const isEmpty = await emptyState.isVisible().catch(() => false)
    const isError = await errorState.isVisible().catch(() => false)
    const hasContent = await page.getByRole('heading', { level: 1 }).isVisible().catch(() => false)

    expect(isEmpty || isError || hasContent).toBe(true)
  })

  test('shows tab triggers when manifest data is present', async ({ authenticatedPage: page }) => {
    const isEmpty = await page.getByTestId('explorer-empty').isVisible().catch(() => false)
    const isError = await page.getByTestId('explorer-error').isVisible().catch(() => false)
    test.skip(isEmpty || isError, 'Requires manifest data to validate tab triggers')

    await expect(page.getByRole('tab', { name: 'Event Channels' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Sagas' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'API Endpoints' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Resources' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Gateway' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Config' })).toBeVisible()
  })

  test('empty state shows guidance message', async ({ authenticatedPage: page }) => {
    const isVisible = await page.getByTestId('explorer-empty').isVisible().catch(() => false)
    test.skip(!isVisible, 'Requires explorer-empty state to validate guidance message')

    await expect(page.getByText('No economy configured')).toBeVisible()
  })
})

test.describe('Economy Edit', () => {
  test.beforeEach(async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/economy/edit')
    // Wait for either loading skeleton or the actual editor content
    await page.waitForSelector('[data-testid="edit-page-loading"], [data-testid="edit-page-error"], h1', { timeout: 15_000 })
  })

  test('renders the Edit Economy page without error', async ({ authenticatedPage: page }) => {
    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
  })

  test('shows Edit Economy heading', async ({ authenticatedPage: page }) => {
    await expect(
      page.getByRole('heading', { name: 'Edit Economy' }),
    ).toBeVisible({ timeout: 15_000 })
  })

  test('shows breadcrumbs with Economy and Edit', async ({ authenticatedPage: page }) => {
    await expect(page.getByText('Economy', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('Edit', { exact: true }).first()).toBeVisible()
  })

  test('loading resolves to editor or error state', async ({ authenticatedPage: page }) => {
    // Loading should eventually resolve
    await expect(page.getByTestId('edit-page-loading')).toHaveCount(0, { timeout: 15_000 })

    // After loading, either the editor renders or error state shows.
    // The heading is always visible (outside the loading/error conditional).
    await expect(
      page.getByRole('heading', { name: 'Edit Economy' }),
    ).toBeVisible()
  })
})

test.describe('Economy navigation flow', () => {
  test('sidebar Economy link navigates to /economy', async ({ authenticatedPage: page }) => {
    const nav = page.getByRole('navigation', { name: 'Main navigation' })
    // Economy is in a collapsible group — expand it if collapsed
    const economyToggle = nav.getByRole('button', { name: /economy/i })
    const isExpanded = await economyToggle.getAttribute('aria-expanded')
    if (isExpanded === 'false') {
      await economyToggle.click()
    }

    const overviewLink = nav.getByRole('link', { name: 'Overview' })
    await overviewLink.click()

    await expect(page).toHaveURL('/economy')
  })

  test('navigating between economy pages preserves auth', async ({ authenticatedPage: page }) => {
    // Navigate to /economy
    await navigateTo(page, '/economy')
    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()

    // Navigate to /economy/edit
    await navigateTo(page, '/economy/edit')
    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
    await expect(
      page.getByRole('heading', { name: 'Edit Economy' }),
    ).toBeVisible({ timeout: 15_000 })

    // Navigate to /economy/explore
    await navigateTo(page, '/economy/explore')
    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
  })
})

test.describe('Economy as platform-admin', () => {
  test('can access /economy as platform-admin', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/economy')
    await page.waitForSelector('[data-testid="overview-loading"], [data-testid="overview-empty"], [data-testid="overview-error"], h1', { timeout: 15_000 })

    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
  })

  test('can access /economy/edit as platform-admin', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/economy/edit')
    await expect(
      page.getByRole('heading', { name: 'Edit Economy' }),
    ).toBeVisible({ timeout: 15_000 })
  })

  test('can access /economy/explore as platform-admin', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/economy/explore')
    await page.waitForSelector('[data-testid="explorer-loading"], [data-testid="explorer-empty"], [data-testid="explorer-error"], h1', { timeout: 15_000 })

    await expect(page.getByText(/Something went wrong/i)).not.toBeVisible()
  })
})
