import { test as base, type Page } from '@playwright/test'

/**
 * Build a dev-mode JWT that satisfies the AuthProvider's parseJWT validation.
 * These tokens are only accepted in DEV mode (import.meta.env.DEV).
 */
function buildDevToken(role: 'platform-admin' | 'tenant-user'): string {
  const header = btoa(JSON.stringify({ alg: 'none', typ: 'JWT' }))
  const payload = btoa(
    JSON.stringify({
      userId: 'e2e-user',
      tenantId: role === 'tenant-user' ? 'dev-tenant' : undefined,
      roles: [role],
      scopes: ['read', 'write'],
      // 24 hours from now
      exp: Math.floor(Date.now() / 1000) + 86_400,
      iss: 'meridian-dev',
      aud: 'meridian-console',
      sub: 'e2e-user',
    }),
  )
  return `${header}.${payload}.e2e-signature`
}

/**
 * Inject a dev auth token via window.__DEV_LOGIN__ exposed by AuthProvider in DEV mode.
 *
 * Auth tokens are memory-only (not persisted). After calling this, use
 * navigateTo() for client-side navigation to preserve the in-memory auth state.
 * Avoid page.goto() for subsequent navigations.
 *
 * After injecting the token, triggers client-side navigation to '/' so
 * ProtectedRoute renders the authenticated content instead of redirecting to /login.
 */
async function injectDevAuth(page: Page, role: 'platform-admin' | 'tenant-user') {
  const token = buildDevToken(role)
  // Start at / which redirects to /login via ProtectedRoute (unauthenticated)
  await page.goto('/')
  await page.waitForFunction(() => typeof (window as Record<string, unknown>).__DEV_LOGIN__ === 'function')
  // Set the in-memory auth token
  await page.evaluate((t) => {
    ;(window as Record<string, unknown>).__DEV_LOGIN__(t)
  }, token)
  // Navigate to / via client-side routing so ProtectedRoute renders the authenticated app.
  // Using history.pushState + popstate event keeps the memory auth state intact.
  await page.evaluate(() => {
    window.history.pushState({}, '', '/')
    window.dispatchEvent(new PopStateEvent('popstate'))
  })
}

/**
 * Navigate to a path using client-side routing (React Router history.pushState).
 * Use this instead of page.goto() after authentication to preserve memory-only auth tokens.
 */
export async function navigateTo(page: Page, path: string) {
  await page.evaluate((p) => {
    window.history.pushState({}, '', p)
    // Dispatch popstate so React Router picks up the new URL
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, path)
}

type Fixtures = {
  authenticatedPage: Page
  platformAdminPage: Page
}

export const test = base.extend<Fixtures>({
  authenticatedPage: async ({ page }, use) => {
    await injectDevAuth(page, 'tenant-user')
    await use(page)
  },
  platformAdminPage: async ({ page }, use) => {
    await injectDevAuth(page, 'platform-admin')
    await use(page)
  },
})

export { expect } from '@playwright/test'
