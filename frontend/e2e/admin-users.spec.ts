import type { Page } from '@playwright/test'
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
    await expect(page.getByRole('columnheader', { name: 'Last Login' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Created' })).toBeVisible()
  })

  test('shows status filter dropdown', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    await expect(page.getByRole('combobox', { name: /status/i })).toBeVisible()
  })

  test('platform admin appears in the users list after login', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    // Wait for the list to settle: a tbody row appears for both real data and the empty state
    const firstRow = page.locator('table tbody tr').first()
    await expect(firstRow).toBeVisible({ timeout: 15_000 })

    // If no users seeded, the empty state row is still a valid outcome
    const emptyState = page.getByRole('row', { name: /no users found/i })
    if (await emptyState.isVisible()) return

    // At least one real user row visible
    const firstEmailCell = firstRow.locator('td').first()
    await expect(firstEmailCell).not.toBeEmpty()
  })

  test('tenant users are listed in the table when backend returns data', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    // Wait for list to settle before inspecting row count
    const firstRow = page.locator('table tbody tr').first()
    await expect(firstRow).toBeVisible({ timeout: 15_000 })

    const emptyState = page.getByRole('row', { name: /no users found/i })
    if (await emptyState.isVisible()) {
      // No users seeded — table renders without error
      return
    }

    // At least one user row visible — verify it has an email cell
    await expect(firstRow).toBeVisible()
    // Email column renders a value (non-empty cell)
    const firstEmailCell = firstRow.locator('td').first()
    await expect(firstEmailCell).not.toBeEmpty()
  })

  test('row click navigates to user detail page', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/users')
    // Wait for list to settle before checking row count
    const firstRow = page.locator('table tbody tr').first()
    await expect(firstRow).toBeVisible({ timeout: 15_000 })

    const emptyState = page.getByRole('row', { name: /no users found/i })
    if (await emptyState.isVisible()) {
      test.skip()
      return
    }

    await firstRow.click()
    await expect(page).toHaveURL(/\/users\/[a-zA-Z0-9-]+/)
  })
})

// ─── User detail — suspend dialog ─────────────────────────────────────────────

test.describe('User detail - suspend dialog', () => {
  /**
   * Navigate to the first available user detail page.
   * Returns false if no users are present (caller should skip the test).
   * Waits for the list to settle before checking for rows to avoid acting
   * before the API response arrives.
   */
  async function navigateToFirstUser(page: Page): Promise<boolean> {
    await navigateTo(page, '/users')
    const firstRow = page.locator('table tbody tr').first()
    await expect(firstRow).toBeVisible({ timeout: 15_000 })
    const emptyState = page.getByRole('row', { name: /no users found/i })
    if (await emptyState.isVisible()) return false
    await firstRow.click()
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
    // Wait for detail page to settle before checking if suspend button is present
    await page.waitForLoadState('networkidle').catch(() => {/* ignore timeout */})
    if (await suspendBtn.isHidden()) {
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
    await page.waitForLoadState('networkidle').catch(() => {/* ignore timeout */})
    if (await suspendBtn.isHidden()) { test.skip(); return }

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
    await page.waitForLoadState('networkidle').catch(() => {/* ignore timeout */})
    if (await suspendBtn.isHidden()) { test.skip(); return }

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
 *
 * Connect protocol unary responses use Content-Type: application/json
 * (not application/connect+json — that's for streaming only).
 * See @connectrpc/connect content-type.ts: contentTypeUnaryJson = "application/json".
 * Error responses also use application/json per the Connect spec.
 */
async function mockListIdentities(page: Page) {
  await page.route(/IdentityService\/ListIdentities/, (route) => {
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
            createdAt: '2023-11-03T12:26:40Z',
            updatedAt: '2023-11-15T12:26:40Z',
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
async function mockRetrieveIdentity(page: Page) {
  await page.route(/IdentityService\/RetrieveIdentity/, (route) => {
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
          createdAt: '2023-11-03T12:26:40Z',
          updatedAt: '2023-11-15T12:26:40Z',
          version: 1,
        },
      }),
    })
  })
}

/**
 * Mock ListRoleAssignments to return empty roles.
 */
async function mockListRoleAssignments(page: Page) {
  await page.route(/IdentityService\/ListRoleAssignments/, (route) => {
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
    await page.route(/IdentityService\/SuspendIdentity/, (route) => {
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
  test('closes dialog and shows suspended state after successful suspend', async ({ platformAdminPage: page }) => {
    await mockListIdentities(page)
    await mockListRoleAssignments(page)

    // First RetrieveIdentity returns ACTIVE, post-suspend returns SUSPENDED
    let suspendCalled = false
    await page.route(/IdentityService\/RetrieveIdentity/, (route) => {
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
              createdAt: '2023-11-03T12:26:40Z',
              updatedAt: '2023-11-15T12:26:41Z',
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
              createdAt: '2023-11-03T12:26:40Z',
              updatedAt: '2023-11-15T12:26:40Z',
              version: 1,
            },
          }),
        })
      }
    })

    await page.route(/IdentityService\/SuspendIdentity/, (route) => {
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
            createdAt: '2023-11-03T12:26:40Z',
            updatedAt: '2023-11-15T12:26:41Z',
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

    // Post-suspend: the page refetches and renders the SUSPENDED status badge.
    // The Suspend button disappears (user is no longer ACTIVE).
    await expect(page.getByRole('button', { name: /^suspend$/i })).toBeHidden({ timeout: 10_000 })
    // The SUSPENDED status badge must be visible after the query invalidation.
    await expect(page.getByText('SUSPENDED')).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('Suspend API — 401 unauthenticated', () => {
  test('401 from SuspendIdentity clears auth and redirects to /login', async ({ platformAdminPage: page }) => {
    await mockListIdentities(page)
    await mockRetrieveIdentity(page)
    await mockListRoleAssignments(page)

    // Mock SuspendIdentity to return 401 Unauthenticated
    await page.route(/IdentityService\/SuspendIdentity/, (route) => {
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
    // ProtectedRoute redirects to /login.
    await expect(page).toHaveURL('/login', { timeout: 10_000 })
  })

  test('session expiry toast appears on 401 before redirect', async ({ platformAdminPage: page }) => {
    await mockListIdentities(page)
    await mockRetrieveIdentity(page)
    await mockListRoleAssignments(page)

    await page.route(/IdentityService\/SuspendIdentity/, (route) => {
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

    // The session expiry error toast fires from withToastErrorHandling.
    // Capture the toast before the redirect clears the DOM.
    await expect(
      page.getByText(/your session has expired/i),
    ).toBeVisible({ timeout: 5_000 })

    // Auth state clears and ProtectedRoute redirects to /login
    await expect(page).toHaveURL('/login', { timeout: 10_000 })
    // Login page renders after redirect
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
