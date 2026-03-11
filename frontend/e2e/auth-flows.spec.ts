import { test, expect } from '@playwright/test'
import { test as authTest, expect as authExpect, navigateTo, buildDevToken } from './fixtures'

/**
 * Auth flow E2E tests covering:
 * - Role normalization (UPPERCASE roles work correctly)
 * - SSO BFF redirect wiring
 * - Callback page token extraction and error handling
 *
 * Login page basics and post-login navigation are covered in login-redirect.spec.ts.
 */

test.describe('Role normalization', () => {
  authTest(
    'UPPERCASE roles are normalized - platform admin can access dashboard',
    async ({ page }) => {
      const token = buildDevToken('platform-admin', { uppercaseRoles: true })

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

      await authExpect(page.locator('main')).toBeVisible({ timeout: 10_000 })
      await authExpect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
    },
  )

  authTest(
    'UPPERCASE roles are normalized - tenant user sees tenant context',
    async ({ page }) => {
      const token = buildDevToken('tenant-user', { uppercaseRoles: true })

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

      await authExpect(page.locator('main')).toBeVisible({ timeout: 10_000 })
      await authExpect(page.getByText(/Overview for dev-tenant/)).toBeVisible({ timeout: 15_000 })
    },
  )

  authTest(
    'UPPERCASE platform-admin role grants access to tenant list',
    async ({ page }) => {
      const token = buildDevToken('platform-admin', { uppercaseRoles: true })

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

      await authExpect(page.locator('main')).toBeVisible({ timeout: 10_000 })

      await navigateTo(page, '/tenants')
      await authExpect(
        page.getByRole('heading', { level: 1 }).filter({ hasText: /Tenant/i }),
      ).toBeVisible({ timeout: 10_000 })
    },
  )
})

test.describe('SSO BFF redirect', () => {
  test.skip('SSO button redirects to BFF endpoint', () => {
    // Requires a real backend with auth providers configured.
    // The useOAuthFlow hook redirects to /api/auth/sso/{connector_id}
    // which is a server-side endpoint that initiates the PKCE flow.
    // Cannot test without a running BFF server.
    // SSO provider buttons only render when the /api/auth/providers API
    // returns OIDC providers, which requires a live backend with Dex.
  })
})

test.describe('Callback page - token from fragment', () => {
  authTest('callback with valid token in fragment logs user in', async ({ page }) => {
    const token = buildDevToken('tenant-user')

    // Navigate to callback with token in fragment (encode to handle +/=/chars)
    await page.goto(`/callback#access_token=${encodeURIComponent(token)}`)

    // Should process the token and redirect to dashboard
    await authExpect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible({
      timeout: 10_000,
    })
    await authExpect(page.getByText(/Overview for dev-tenant/)).toBeVisible({ timeout: 15_000 })
  })

  test('callback with no token shows error message', async ({ page }) => {
    await page.goto('/callback')

    await expect(page.getByText('Authentication Failed')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('No authentication token received')).toBeVisible()
  })

  test('callback with error param shows error description', async ({ page }) => {
    await page.goto('/callback?error=access_denied&error_description=User+denied+access')

    await expect(page.getByText('Authentication Failed')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('User denied access')).toBeVisible()
  })

  test('callback error page return-to-login button navigates to /login', async ({ page }) => {
    await page.goto('/callback?error=server_error')

    await expect(page.getByText('Authentication Failed')).toBeVisible({ timeout: 10_000 })
    const returnButton = page.getByRole('button', { name: 'Return to Login' })
    await expect(returnButton).toBeVisible()

    await returnButton.click()
    await expect(page).toHaveURL('/login', { timeout: 10_000 })
    await expect(page.getByRole('heading', { name: 'Meridian Operations Console' })).toBeVisible()
  })
})

test.describe('Login page - password form', () => {
  test('password form is hidden in dev mode (shown only in demo/production)', async ({ page }) => {
    // The password form is only rendered when VITE_DEMO_MODE=true or not in DEV mode.
    // In the E2E dev server, it should NOT be present.
    await page.goto('/login')

    // Dev login buttons are always present in dev/E2E mode
    await expect(page.getByRole('button', { name: /platform.admin/i })).toBeVisible()
    await expect(page.getByRole('button', { name: /tenant.user/i })).toBeVisible()

    // Password form should not be rendered in standard dev mode
    await expect(page.locator('input[type="email"]')).toHaveCount(0)
    await expect(page.locator('input[type="password"]')).toHaveCount(0)
  })
})
