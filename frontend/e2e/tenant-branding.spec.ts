import { test, expect, type Page } from '@playwright/test'
import { test as authTest, expect as authExpect } from './fixtures'

/**
 * Build a dev-mode JWT with an explicit tenant display name claim.
 * The auth context reads `x-tenant-display-name` to populate `claims.tenantDisplayName`.
 */
function buildTokenWithDisplayName(displayName: string): string {
  const header = btoa(JSON.stringify({ alg: 'none', typ: 'JWT' }))
  const payload = btoa(
    JSON.stringify({
      userId: 'e2e-user',
      tenantId: 'dev-tenant',
      roles: ['tenant-user'],
      scopes: ['read', 'write'],
      'x-tenant-display-name': displayName,
      exp: Math.floor(Date.now() / 1000) + 86_400,
      iss: 'meridian-dev',
      aud: 'meridian-console',
      sub: 'e2e-user',
    }),
  )
  return `${header}.${payload}.e2e-signature`
}

/**
 * Inject a dev token via __DEV_LOGIN__ and navigate to / so the authenticated
 * app shell renders with the given token's claims.
 */
async function injectTokenAndNavigate(page: Page, token: string) {
  await page.goto('/')
  await page.waitForFunction(
    () => typeof (window as Record<string, unknown>).__DEV_LOGIN__ === 'function',
  )
  await page.evaluate((t) => {
    ;(window as Record<string, unknown>).__DEV_LOGIN__(t)
  }, token)
  await page.evaluate(() => {
    window.history.pushState({}, '', '/')
    window.dispatchEvent(new PopStateEvent('popstate'))
  })
  await page.waitForSelector('main', { timeout: 10_000 })
}

/**
 * Tenant branding E2E tests.
 *
 * The test environment runs on localhost, which has no tenant subdomain.
 * Tenant subdomain scenarios are simulated by mocking the /api/tenant-info
 * endpoint via page.route().
 *
 * Scenarios covered:
 * - Bare domain login page heading and browser title
 * - Tenant subdomain login page heading and browser title (mocked API)
 * - Header shows tenant display name from JWT after login
 * - Header shows "Meridian" when no display name is present
 */

test.describe('Tenant branding - bare domain login page', () => {
  test('shows "Meridian Operations Console" heading', async ({ page }) => {
    await page.goto('/login')
    await expect(page.getByRole('heading', { name: 'Meridian Operations Console' })).toBeVisible()
  })

  test('browser title is "Meridian Operations Console"', async ({ page }) => {
    await page.goto('/login')
    // Wait for the heading to confirm the page has settled
    await page.getByRole('heading', { name: 'Meridian Operations Console' }).waitFor()
    await expect(page).toHaveTitle('Meridian Operations Console')
  })
})

test.describe('Tenant branding - tenant subdomain login page', () => {
  test('shows tenant display name in login heading', async ({ page }) => {
    await page.route('/api/tenant-info', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ slug: 'acme', displayName: 'Acme Energy' }),
      }),
    )
    await page.goto('/login')
    await expect(
      page.getByRole('heading', { name: 'Acme Energy Operations Console' }),
    ).toBeVisible()
  })

  test('browser title shows "{Tenant Name} - Operations Console"', async ({ page }) => {
    await page.route('/api/tenant-info', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ slug: 'acme', displayName: 'Acme Energy' }),
      }),
    )
    await page.goto('/login')
    await page.getByRole('heading', { name: 'Acme Energy Operations Console' }).waitFor()
    await expect(page).toHaveTitle('Acme Energy - Operations Console')
  })
})

test.describe('Tenant branding - header after login', () => {
  test('header shows tenant display name from JWT', async ({ page }) => {
    const token = buildTokenWithDisplayName('Volterra Energy')
    await injectTokenAndNavigate(page, token)
    await expect(page.locator('header').getByText('Volterra Energy')).toBeVisible()
  })

  authTest('header shows "Meridian" for platform admin with no tenant selected', async ({
    platformAdminPage: page,
  }) => {
    // Platform admin token has no tenantId and no x-tenant-display-name.
    // With no tenant selected in the UI, tenantSlug is null -> falls back to "Meridian".
    await authExpect(page.locator('header').getByText('Meridian')).toBeVisible()
  })
})
