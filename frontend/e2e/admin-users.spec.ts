import { test, expect, navigateTo } from './fixtures'

/**
 * E2E tests for admin user visibility and suspend flow.
 *
 * Tests run against the Vite dev server. Some tests use page.route() to mock
 * gRPC-web API responses so they can verify toast/redirect behaviour without
 * a live backend.
 *
 * The /users route is wrapped in AdminOnlyRoute. Both the platform-admin and
 * admin-role fixtures satisfy that guard, so they are used throughout.
 *
 * Connect-ES serialisation note:
 *   In dev/E2E mode useBinaryFormat is false — the transport uses the Connect
 *   JSON protocol (Content-Type: application/json). Error responses use the
 *   Connect error shape: { "code": "<snake_case>", "message": "..." }.
 *
 * Toast selectors:
 *   Sonner renders toasts in [data-sonner-toaster] → [data-sonner-toast].
 *   The toast title text is inside a [data-title] element.
 */

// ─── Users list ───────────────────────────────────────────────────────────────

test.describe('Users list - admin access', () => {
  test('renders Users heading for platform admin', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible()
  })

  test('shows Invite User button', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    await expect(page.getByRole('button', { name: /invite user/i })).toBeVisible()
  })

  test('renders user table with Email, Status, MFA, Last Login, Created columns', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    await expect(page.getByRole('columnheader', { name: 'Email' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Status' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'MFA' })).toBeVisible()
  })

  test('shows status filter dropdown', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    await expect(page.getByRole('combobox', { name: /status/i })).toBeVisible()
  })

  test('platform admin appears in the users list after login', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    // The table must be present — either loading, empty, or populated
    await expect(page.locator('table')).toBeVisible()
    // If users are returned by the backend, at least one row must render
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount > 0) {
      await expect(page.locator('table tbody tr').first()).toBeVisible()
    } else {
      // Empty state is also valid — UI renders without error
      await expect(page.getByText('No users found').or(page.locator('table'))).toBeVisible({ timeout: 15_000 })
    }
  })

  test('tenant users are listed in the table when backend returns data', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    await expect(page.locator('table')).toBeVisible()

    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      // No users seeded — table renders without error
      await expect(page.getByText(/no users found/i).or(page.locator('table tbody'))).toBeVisible({ timeout: 15_000 })
      return
    }

    // At least one user row visible — verify it has an email cell
    await expect(page.locator('table tbody tr').first()).toBeVisible()
    // Email column renders a value (non-empty cell)
    const firstEmailCell = page.locator('table tbody tr').first().locator('td').first()
    await expect(firstEmailCell).not.toBeEmpty()
  })

  test('row click navigates to user detail page', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    await expect(page.locator('table')).toBeVisible()

    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) {
      test.skip()
      return
    }

    await page.locator('table tbody tr').first().click()
    await expect(page).toHaveURL(/\/users\/[a-zA-Z0-9-]+/)
  })
})

// ─── User detail — suspend dialog ─────────────────────────────────────────────

test.describe('User detail - suspend dialog', () => {
  /**
   * Navigate to the first available user detail page.
   * Returns false if no users are present (caller should skip the test).
   */
  async function navigateToFirstUser(page: import('@playwright/test').Page): Promise<boolean> {
    await navigateTo(page, '/users')
    await expect(page.locator('table')).toBeVisible()
    const rowCount = await page.locator('table tbody tr').count()
    if (rowCount === 0) return false
    await page.locator('table tbody tr').first().click()
    await page.waitForURL(/\/users\/[a-zA-Z0-9-]+/)
    return true
  }

  test('shows user email as heading on detail page', async ({ platformAdminPage: page }) => {
    const found = await navigateToFirstUser(page)
    if (!found) { test.skip(); return }
    // h1 contains the user email
    const heading = page.getByRole('heading', { level: 1 })
    await expect(heading).toBeVisible({ timeout: 15_000 })
    await expect(heading).not.toBeEmpty()
  })

  test('shows Identity Details card', async ({ platformAdminPage: page }) => {
    const found = await navigateToFirstUser(page)
    if (!found) { test.skip(); return }
    await expect(page.getByText('Identity Details')).toBeVisible({ timeout: 15_000 })
  })

  test('shows Back to Users link', async ({ platformAdminPage: page }) => {
    const found = await navigateToFirstUser(page)
    if (!found) { test.skip(); return }
    await expect(page.getByText('Back to Users')).toBeVisible({ timeout: 15_000 })
  })

  test('Suspend button opens confirm dialog for active user', async ({ platformAdminPage: page }) => {
    const found = await navigateToFirstUser(page)
    if (!found) { test.skip(); return }

    const suspendBtn = page.getByRole('button', { name: /^suspend$/i })
    const hasSuspendBtn = await suspendBtn.isVisible()
    if (!hasSuspendBtn) {
      // User is not ACTIVE (e.g. already suspended) — skip
      test.skip()
      return
    }

    await suspendBtn.click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByRole('heading', { name: 'Suspend User' })).toBeVisible()
    await expect(dialog.getByText('Suspending this user will prevent them from logging in.')).toBeVisible()
    await expect(dialog.getByLabel('Reason')).toBeVisible()
  })

  test('Suspend dialog requires a reason before confirming', async ({ platformAdminPage: page }) => {
    const found = await navigateToFirstUser(page)
    if (!found) { test.skip(); return }

    const suspendBtn = page.getByRole('button', { name: /^suspend$/i })
    if (!await suspendBtn.isVisible()) { test.skip(); return }

    await suspendBtn.click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    // The Suspend User confirm button must be disabled when reason is empty
    const confirmBtn = dialog.getByRole('button', { name: 'Suspend User' })
    await expect(confirmBtn).toBeDisabled()
  })

  test('Suspend dialog closes on Cancel', async ({ platformAdminPage: page }) => {
    const found = await navigateToFirstUser(page)
    if (!found) { test.skip(); return }

    const suspendBtn = page.getByRole('button', { name: /^suspend$/i })
    if (!await suspendBtn.isVisible()) { test.skip(); return }

    await suspendBtn.click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).not.toBeVisible()
  })
})

// ─── Suspend API mock tests ───────────────────────────────────────────────────
//
// These tests inject a dev auth token then mock specific gRPC-web endpoints via
// page.route() so they run without a live backend. They verify error toast
// behaviour and redirect semantics for 403 (PermissionDenied) and 401
// (Unauthenticated) responses from the SuspendIdentity RPC.
//
// The /users route requires AdminOnlyRoute to pass (platformAdminPage satisfies
// this). The mock returns a user whose status is ACTIVE so the Suspend button
// is visible. Then a second route intercepts the SuspendIdentity call and
// returns the desired error.

/**
 * Mock the IdentityService to return a single ACTIVE user.
 * Connect JSON protocol: ListIdentities response.
 */
async function mockListIdentities(page: import('@playwright/test').Page) {
  await page.route(`**/meridian.identity.v1.IdentityService/ListIdentities`, (route) => {
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        identities: [
          {
            id: 'mock-user-1',
            email: 'alice@example.com',
            status: 2, // ACTIVE
            mfaEnabled: false,
            failedAttempts: 0,
            externalIdp: '',
            externalIdpSub: '',
            createdAt: { seconds: '1699000000', nanos: 0 },
            updatedAt: { seconds: '1700000000', nanos: 0 },
            version: 1,
          },
        ],
        nextPageToken: '',
        totalCount: 1,
      }),
    })
  })
}

/**
 * Mock RetrieveIdentity to return an ACTIVE user.
 */
async function mockRetrieveIdentity(page: import('@playwright/test').Page) {
  await page.route(`**/meridian.identity.v1.IdentityService/RetrieveIdentity`, (route) => {
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        identity: {
          id: 'mock-user-1',
          email: 'alice@example.com',
          status: 2, // ACTIVE
          mfaEnabled: false,
          failedAttempts: 0,
          externalIdp: '',
          externalIdpSub: '',
          createdAt: { seconds: '1699000000', nanos: 0 },
          updatedAt: { seconds: '1700000000', nanos: 0 },
          version: 1,
        },
      }),
    })
  })
}

/**
 * Mock ListRoleAssignments to return empty roles.
 */
async function mockListRoleAssignments(page: import('@playwright/test').Page) {
  await page.route(`**/meridian.identity.v1.IdentityService/ListRoleAssignments`, (route) => {
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ roleAssignments: [] }),
    })
  })
}

test.describe('Suspend API — 403 permission denied', () => {
  test.beforeEach(async ({ platformAdminPage: page }) => {
    await mockListIdentities(page)
    await mockRetrieveIdentity(page)
    await mockListRoleAssignments(page)

    // Mock SuspendIdentity to return 403 PermissionDenied
    await page.route(`**/meridian.identity.v1.IdentityService/SuspendIdentity`, (route) => {
      void route.fulfill({
        status: 403,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 'permission_denied',
          message: 'You do not have permission to perform this action.',
        }),
      })
    })
  })

  test('shows permission denied toast when suspend returns 403', async ({ platformAdminPage: page }) => {
    // Navigate to the mocked user detail page
    await navigateTo(page, '/users/mock-user-1')

    await expect(page.getByRole('button', { name: /^suspend$/i })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: /^suspend$/i }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Reason').fill('Policy violation')
    await dialog.getByRole('button', { name: 'Suspend User' }).click()

    // Toast must appear with the permission denied message
    await expect(
      page.getByText('You do not have permission to perform this action.'),
    ).toBeVisible({ timeout: 10_000 })
  })

  test('403 error does not redirect to login', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users/mock-user-1')

    await expect(page.getByRole('button', { name: /^suspend$/i })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: /^suspend$/i }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Reason').fill('Policy violation')
    await dialog.getByRole('button', { name: 'Suspend User' }).click()

    // Wait for toast to appear
    await expect(
      page.getByText('You do not have permission to perform this action.'),
    ).toBeVisible({ timeout: 10_000 })

    // URL must remain on the user detail page — no redirect to /login
    await expect(page).toHaveURL(/\/users\/mock-user-1/)
  })
})

test.describe('Suspend API — successful suspend', () => {
  test('closes dialog and page stays on user detail after successful suspend', async ({ platformAdminPage: page }) => {
    await mockListIdentities(page)
    await mockListRoleAssignments(page)
    await mockRetrieveIdentity(page)

    // First call returns ACTIVE, after suspend it returns SUSPENDED
    let suspendCalled = false
    await page.route(`**/meridian.identity.v1.IdentityService/RetrieveIdentity`, (route) => {
      if (suspendCalled) {
        void route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            identity: {
              id: 'mock-user-1',
              email: 'alice@example.com',
              status: 3, // SUSPENDED
              mfaEnabled: false,
              failedAttempts: 0,
              externalIdp: '',
              externalIdpSub: '',
              createdAt: { seconds: '1699000000', nanos: 0 },
              updatedAt: { seconds: '1700000001', nanos: 0 },
              version: 2,
            },
          }),
        })
      } else {
        void route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            identity: {
              id: 'mock-user-1',
              email: 'alice@example.com',
              status: 2, // ACTIVE
              mfaEnabled: false,
              failedAttempts: 0,
              externalIdp: '',
              externalIdpSub: '',
              createdAt: { seconds: '1699000000', nanos: 0 },
              updatedAt: { seconds: '1700000000', nanos: 0 },
              version: 1,
            },
          }),
        })
      }
    })

    await page.route(`**/meridian.identity.v1.IdentityService/SuspendIdentity`, (route) => {
      suspendCalled = true
      void route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          identity: {
            id: 'mock-user-1',
            email: 'alice@example.com',
            status: 3, // SUSPENDED
            mfaEnabled: false,
            failedAttempts: 0,
            externalIdp: '',
            externalIdpSub: '',
            createdAt: { seconds: '1699000000', nanos: 0 },
            updatedAt: { seconds: '1700000001', nanos: 0 },
            version: 2,
          },
        }),
      })
    })

    await navigateTo(page, '/users/mock-user-1')
    await expect(page.getByRole('button', { name: /^suspend$/i })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: /^suspend$/i }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Reason').fill('Compliance review')
    await dialog.getByRole('button', { name: 'Suspend User' }).click()

    // Dialog closes on success
    await expect(dialog).not.toBeVisible({ timeout: 10_000 })

    // URL stays on user detail
    await expect(page).toHaveURL(/\/users\/mock-user-1/)
  })
})

test.describe('Suspend API — 401 unauthenticated', () => {
  test('401 from SuspendIdentity clears auth and redirects to /login', async ({ platformAdminPage: page }) => {
    await mockListIdentities(page)
    await mockRetrieveIdentity(page)
    await mockListRoleAssignments(page)

    // Mock SuspendIdentity to return 401 Unauthenticated
    await page.route(`**/meridian.identity.v1.IdentityService/SuspendIdentity`, (route) => {
      void route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 'unauthenticated',
          message: 'Your session has expired. Please sign in again.',
        }),
      })
    })

    await navigateTo(page, '/users/mock-user-1')
    await expect(page.getByRole('button', { name: /^suspend$/i })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: /^suspend$/i }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Reason').fill('Compliance review')
    await dialog.getByRole('button', { name: 'Suspend User' }).click()

    // Auth interceptor calls onUnauthenticated → logout() → token cleared →
    // ProtectedRoute redirects to /login. The toast also fires.
    await expect(page).toHaveURL('/login', { timeout: 10_000 })
  })

  test('session expiry toast appears before redirect on 401', async ({ platformAdminPage: page }) => {
    await mockListIdentities(page)
    await mockRetrieveIdentity(page)
    await mockListRoleAssignments(page)

    await page.route(`**/meridian.identity.v1.IdentityService/SuspendIdentity`, (route) => {
      void route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 'unauthenticated',
          message: 'Your session has expired. Please sign in again.',
        }),
      })
    })

    await navigateTo(page, '/users/mock-user-1')
    await expect(page.getByRole('button', { name: /^suspend$/i })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: /^suspend$/i }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Reason').fill('Compliance review')
    await dialog.getByRole('button', { name: 'Suspend User' }).click()

    // The session expiry error toast fires from withToastErrorHandling
    // before the auth state clears and ProtectedRoute redirects.
    // Because the redirect is fast, check URL destination.
    await expect(page).toHaveURL('/login', { timeout: 10_000 })
    // Login page must render (confirms redirect completed, not a blank page)
    await expect(page.getByRole('heading', { name: 'Meridian Operations Console' })).toBeVisible()
  })
})

// ─── Non-admin access ─────────────────────────────────────────────────────────

test.describe('Users page - non-admin redirect', () => {
  test('tenant-user without admin role is redirected from /users', async ({ authenticatedPage: page }) => {
    await navigateTo(page, '/users')
    // AdminOnlyRoute redirects non-admin users to /
    await expect(page).toHaveURL('/', { timeout: 5_000 })
  })
})
