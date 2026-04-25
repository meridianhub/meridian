import { test, expect, type Page } from '@playwright/test'
import { test as authTest, expect as authExpect, navigateTo } from './fixtures'

/**
 * Self-Service Onboarding capstone E2E test.
 *
 * Exercises the vertical slice produced by the self-service-onboarding-readiness
 * workstream (PRD-062, tasks 1-9): registration form UX, password policy
 * alignment, async provisioning gating, and the audit log empty state that
 * recedes once activity is recorded.
 *
 * Why route mocking instead of a live registration round-trip:
 *   The Vite preview server only proxies the gRPC-web prefix (/meridian.*) and
 *   /v1 to the meridian backend - the BFF endpoints used by registration and
 *   password login (/api/v1/register, /api/v1/slugs/{slug}/available,
 *   /api/v1/provisioning-status, /api/auth/login) are not proxied. The login
 *   page also gates the password form on `!isBaseDomain(hostname)`, and
 *   `localhost` is a base domain. Together these mean a true
 *   register-then-password-login round trip cannot run against the live CI
 *   backend without a frontend infrastructure change.
 *
 *   We mock the BFF endpoints with `page.route()` (mirroring the pattern
 *   established by admin-users.spec.ts) so this test exercises the full
 *   registration UI state machine - slug check, sync vs async provisioning,
 *   provisioning progress UX, and post-redirect navigation - against the real
 *   bundled frontend. The post-login segment uses the same dev-token fixture
 *   the rest of the suite uses, then mocks the AuditService so the empty
 *   state vs populated state assertions are deterministic.
 *
 * Slug/email values include `Date.now()` so each run is unique and idempotent.
 */

// ---------------------------------------------------------------------------
// Helpers: mock BFF endpoints used by the registration form
// ---------------------------------------------------------------------------

interface RegisterMockOptions {
  slugAvailable?: boolean
  provisioningPending?: boolean
  loginUrl?: string
  tenantId?: string
  /**
   * Drives the provisioning-status poll responses. Each call to
   * /api/v1/provisioning-status returns the next item; once the array is
   * exhausted the final value repeats. Use `'COMPLETED'` to advance the
   * page to /login.
   */
  provisioningStatusSequence?: ReadonlyArray<'PENDING' | 'IN_PROGRESS' | 'COMPLETED' | 'FAILED'>
}

async function mockRegistrationAPIs(page: Page, options: RegisterMockOptions = {}) {
  const {
    slugAvailable = true,
    provisioningPending = false,
    loginUrl = '/login?tenant=onboarding-test',
    tenantId = 'onboarding-test',
    provisioningStatusSequence,
  } = options

  await page.route(/\/api\/v1\/slugs\/[^/]+\/available/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ available: slugAvailable }),
    })
  })

  await page.route('**/api/v1/register', async (route) => {
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        tenant_id: tenantId,
        login_url: loginUrl,
        provisioning_pending: provisioningPending,
        verification_required: false,
      }),
    })
  })

  if (provisioningStatusSequence && provisioningStatusSequence.length > 0) {
    let callIndex = 0
    await page.route(/\/api\/v1\/provisioning-status/, async (route) => {
      const i = Math.min(callIndex, provisioningStatusSequence.length - 1)
      callIndex += 1
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ overall: provisioningStatusSequence[i] }),
      })
    })
  }
}

// ---------------------------------------------------------------------------
// Suite 1: registration form UI flow
// ---------------------------------------------------------------------------

test.describe('Self-Service Onboarding - registration form', () => {
  test('slug-availability check surfaces "Slug is available" for a free slug', async ({ page }) => {
    const slug = `e2e-avail-${Date.now()}`
    await mockRegistrationAPIs(page, { slugAvailable: true })

    await page.goto('/register')
    await expect(page.getByRole('heading', { name: 'Create your account' })).toBeVisible()

    await page.locator('#slug').fill(slug)
    await expect(page.getByText('Slug is available')).toBeVisible({ timeout: 10_000 })
  })

  test('slug-availability check surfaces "Slug is already taken" when backend reports collision', async ({ page }) => {
    const slug = `e2e-taken-${Date.now()}`
    await mockRegistrationAPIs(page, { slugAvailable: false })

    await page.goto('/register')
    await page.locator('#slug').fill(slug)
    await expect(page.getByText('Slug is already taken')).toBeVisible({ timeout: 10_000 })
    // Submit button is disabled when slug is taken.
    await expect(page.getByRole('button', { name: 'Create account' })).toBeDisabled()
  })

  test('sub-policy passwords are blocked client-side with the policy hint visible', async ({ page }) => {
    const slug = `e2e-weakpwd-${Date.now()}`
    await mockRegistrationAPIs(page, { slugAvailable: true })

    await page.goto('/register')
    await page.locator('#slug').fill(slug)
    await expect(page.getByText('Slug is available')).toBeVisible({ timeout: 10_000 })

    await page.locator('#email').fill(`weak-${Date.now()}@example.com`)
    await page.locator('#password').fill('weakpass') // 8 chars, no upper, no digit
    await page.getByRole('button', { name: 'Create account' }).click()

    // The form-level validator blocks the submission and surfaces the policy
    // string aligned with the backend (task 2 of self-service-onboarding-readiness).
    await expect(page.getByText(/password must be at least 12 characters/i)).toBeVisible()
    await expect(page.getByText(/at least 12 characters with uppercase/i)).toBeVisible()
    await expect(page).toHaveURL(/\/register/)
  })

  test('synchronous provisioning navigates straight to /login after registration', async ({ page }) => {
    const slug = `e2e-sync-${Date.now()}`
    const email = `e2e-sync-${Date.now()}@example.com`
    const tenantId = `tenant-sync-${Date.now()}`
    await mockRegistrationAPIs(page, {
      provisioningPending: false,
      tenantId,
      loginUrl: `/login?tenant=${slug}`,
    })

    await page.goto('/register')
    await page.locator('#slug').fill(slug)
    await expect(page.getByText('Slug is available')).toBeVisible({ timeout: 10_000 })

    await page.locator('#email').fill(email)
    await page.locator('#password').fill('SecurePass123!')

    await page.getByRole('button', { name: 'Create account' }).click()

    await expect(page).toHaveURL(/\/login/, { timeout: 10_000 })
  })

  test('async provisioning shows the progress UX, then completes and lands on /login', async ({ page }) => {
    const slug = `e2e-async-${Date.now()}`
    const email = `e2e-async-${Date.now()}@example.com`
    const tenantId = `tenant-async-${Date.now()}`
    await mockRegistrationAPIs(page, {
      provisioningPending: true,
      tenantId,
      loginUrl: `/login?tenant=${slug}`,
      // Two pending ticks then COMPLETED. The poll interval is 1 s, so this
      // resolves within a few seconds without arbitrary waits.
      provisioningStatusSequence: ['PENDING', 'PENDING', 'COMPLETED'],
    })

    await page.goto('/register')
    await page.locator('#slug').fill(slug)
    await expect(page.getByText('Slug is available')).toBeVisible({ timeout: 10_000 })

    await page.locator('#email').fill(email)
    await page.locator('#password').fill('SecurePass123!')

    await page.getByRole('button', { name: 'Create account' }).click()

    // ProvisioningProgress is shown while the gating poll runs (task 4).
    await expect(
      page.getByRole('heading', { name: /Setting up your tenant/i }),
    ).toBeVisible({ timeout: 5_000 })

    // Poll eventually reports COMPLETED and the page navigates to /login.
    await expect(page).toHaveURL(/\/login/, { timeout: 20_000 })
  })

  test('async provisioning surfaces the failed state when the saga reports failure', async ({ page }) => {
    const slug = `e2e-fail-${Date.now()}`
    const email = `e2e-fail-${Date.now()}@example.com`
    const tenantId = `tenant-fail-${Date.now()}`
    await mockRegistrationAPIs(page, {
      provisioningPending: true,
      tenantId,
      loginUrl: `/login?tenant=${slug}`,
      provisioningStatusSequence: ['PENDING', 'FAILED'],
    })

    await page.goto('/register')
    await page.locator('#slug').fill(slug)
    await expect(page.getByText('Slug is available')).toBeVisible({ timeout: 10_000 })

    await page.locator('#email').fill(email)
    await page.locator('#password').fill('SecurePass123!')

    await page.getByRole('button', { name: 'Create account' }).click()

    // ProvisioningProgress should transition pending -> failed without timing out.
    await expect(
      page.getByRole('heading', { name: /Setup failed/i }),
    ).toBeVisible({ timeout: 10_000 })
    // We must NOT navigate to /login when provisioning fails - the user stays
    // on the progress screen so they can contact support or retry.
    await expect(page).not.toHaveURL(/\/login/)
  })
})

// ---------------------------------------------------------------------------
// Suite 2: post-login audit log behaviour
// ---------------------------------------------------------------------------
//
// The dev-token fixture stands in for the registration -> login round trip
// because the BFF login endpoint is not proxied through the preview server
// (see file header). What matters here is the slice we actually own: the
// audit log's empty state (task 9) recedes once an audit entry exists, which
// is the contract a freshly-onboarded tenant relies on after their first
// party creation.

interface MockAuditEntry {
  entryId: string
  timestamp: string
  tableName: string
  // Connect-Web deserializes proto3 enums to their full uppercase name when
  // returned over Connect JSON. AUDIT_OPERATION_INSERT = 1 (see audit_events_pb.ts).
  operation: 'AUDIT_OPERATION_INSERT' | 'AUDIT_OPERATION_UPDATE' | 'AUDIT_OPERATION_DELETE'
  recordId: string
  changedBy: string
  oldValues: Record<string, unknown> | null
  newValues: Record<string, unknown> | null
}

async function mockListAuditEntries(page: Page, entries: MockAuditEntry[]) {
  await page.route(/\/meridian\.audit\.v1\.AuditService\/ListAuditEntries/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ entries, nextPageToken: '' }),
    })
  })
}

test.describe('Self-Service Onboarding - audit log empty state', () => {
  authTest('shows empty state with party-creation hint when tenant has no entries', async ({
    authenticatedPage: page,
  }) => {
    await mockListAuditEntries(page, [])

    await navigateTo(page, '/audit-log')
    await authExpect(page.getByRole('heading', { name: 'Audit Log' })).toBeVisible({
      timeout: 15_000,
    })
    await authExpect(page.getByTestId('empty-state')).toBeVisible({ timeout: 15_000 })
    await authExpect(page.getByText(/No audit events yet/i)).toBeVisible()
    // Empty-state copy from task 9 nudges the user toward party creation.
    await authExpect(page.getByText(/Try creating a party to see your first event/i)).toBeVisible()
  })

  authTest('hides empty state and renders the entry once a party has been recorded', async ({
    authenticatedPage: page,
  }) => {
    const partyEntry: MockAuditEntry = {
      entryId: `audit-${Date.now()}`,
      timestamp: '2026-04-25T12:00:00Z',
      tableName: 'party',
      operation: 'AUDIT_OPERATION_INSERT',
      recordId: `party-${Date.now()}`,
      changedBy: 'e2e-user',
      oldValues: null,
      newValues: { display_name: 'E2E Onboarding Party' },
    }
    await mockListAuditEntries(page, [partyEntry])

    await navigateTo(page, '/audit-log')
    await authExpect(page.getByRole('heading', { name: 'Audit Log' })).toBeVisible({
      timeout: 15_000,
    })

    // Empty state must NOT render once entries exist - this is the regression
    // we care about for the onboarding capstone.
    await authExpect(page.getByTestId('empty-state')).toHaveCount(0, { timeout: 15_000 })
    await authExpect(page.getByText(/No audit events yet/i)).toHaveCount(0)

    // The INSERT badge and the party table name from the seeded entry render.
    // Scope to the data table to avoid the hidden filter dropdown options
    // (the Operation filter has an "Insert" <option> that also matches /INSERT/).
    const dataRow = page.locator('table tbody tr').first()
    await authExpect(dataRow).toBeVisible({ timeout: 10_000 })
    await authExpect(dataRow.getByText('INSERT')).toBeVisible()
    await authExpect(dataRow.getByRole('cell', { name: 'party', exact: true })).toBeVisible()
  })
})
