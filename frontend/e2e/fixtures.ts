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
 */
async function injectDevAuth(page: Page, role: 'platform-admin' | 'tenant-user') {
  const token = buildDevToken(role)
  await page.goto('/')
  await page.waitForFunction(() => typeof (window as Record<string, unknown>).__DEV_LOGIN__ === 'function')
  await page.evaluate((t) => {
    ;(window as Record<string, unknown>).__DEV_LOGIN__(t)
  }, token)
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
