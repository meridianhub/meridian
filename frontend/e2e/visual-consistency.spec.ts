import { test, expect, navigateTo } from './fixtures'

/**
 * Structural conformance tests for the canonical page layout.
 *
 * Every feature page should follow the shared layout pattern:
 *   - PageShell: div.space-y-6 wrapper
 *   - PageHeader: h1 with text-3xl font-bold tracking-tight
 *   - List pages: DataTable wrapped in a Card (data-slot="card")
 *   - Detail pages: Breadcrumbs nav + h1 + tab navigation
 *
 * These tests verify structural conformance only — no backend required.
 * Auth tokens are memory-only; all navigation uses navigateTo() to preserve state.
 */

// ─── List pages ────────────────────────────────────────────────────────────────

const LIST_PAGES = [
  { path: '/accounts', heading: 'Accounts' },
  { path: '/parties', heading: 'Parties' },
  { path: '/payments', heading: 'Payments' },
  { path: '/positions', heading: 'Positions' },
  { path: '/reconciliation', heading: 'Reconciliation' },
  { path: '/starlark-config', heading: 'Starlark Configuration' },
  { path: '/ledger', heading: 'Ledger' },
  { path: '/internal-accounts', heading: 'Internal Accounts' },
  { path: '/audit-log', heading: 'Audit Log' },
  { path: '/market-data', heading: 'Market Data' },
  { path: '/gateway-mappings', heading: 'Gateway Mappings' },
] as const

const REFERENCE_DATA_PAGES = [
  { path: '/reference-data/instruments', heading: 'Instruments' },
  { path: '/reference-data/nodes', heading: 'Nodes' },
  { path: '/reference-data/account-types', heading: 'Account Types' },
] as const

const ADMIN_LIST_PAGES = [
  { path: '/users', heading: 'Users' },
] as const

test.describe('Structural conformance — list pages (tenant-user)', () => {
  for (const page of [...LIST_PAGES, ...REFERENCE_DATA_PAGES]) {
    test.describe(page.path, () => {
      test('has PageShell wrapper (space-y-6)', async ({ authenticatedPage }) => {
        await navigateTo(authenticatedPage, page.path)
        // Wait for the heading to confirm page rendered
        await expect(
          authenticatedPage.getByRole('heading', { level: 1 }),
        ).toBeVisible({ timeout: 10_000 })
        // PageShell renders a div.space-y-6 containing the h1
        const shell = authenticatedPage.locator('.space-y-6').filter({
          has: authenticatedPage.getByRole('heading', { level: 1 }),
        })
        await expect(shell).toBeVisible()
      })

      test('has PageHeader h1 with correct styling', async ({ authenticatedPage }) => {
        await navigateTo(authenticatedPage, page.path)
        const h1 = authenticatedPage.getByRole('heading', { level: 1 })
        await expect(h1).toBeVisible({ timeout: 10_000 })
        await expect(h1).toHaveClass(/text-3xl/)
        await expect(h1).toHaveClass(/font-bold/)
        await expect(h1).toHaveClass(/tracking-tight/)
      })

      test('has DataTable inside a Card', async ({ authenticatedPage }) => {
        await navigateTo(authenticatedPage, page.path)
        await expect(
          authenticatedPage.getByRole('heading', { level: 1 }),
        ).toBeVisible({ timeout: 10_000 })
        // Card uses data-slot="card", table is rendered inside it
        const card = authenticatedPage.locator('[data-slot="card"]').first()
        await expect(card).toBeVisible({ timeout: 10_000 })
        // DataTable renders a <table> element
        const table = card.locator('table')
        await expect(table).toBeVisible()
      })
    })
  }
})

test.describe('Structural conformance — list pages (platform-admin)', () => {
  for (const page of ADMIN_LIST_PAGES) {
    test.describe(page.path, () => {
      test('has PageShell wrapper (space-y-6)', async ({ platformAdminPage }) => {
        await navigateTo(platformAdminPage, page.path)
        await expect(
          platformAdminPage.getByRole('heading', { level: 1 }),
        ).toBeVisible({ timeout: 10_000 })
        const shell = platformAdminPage.locator('.space-y-6').filter({
          has: platformAdminPage.getByRole('heading', { level: 1 }),
        })
        await expect(shell).toBeVisible()
      })

      test('has PageHeader h1 with correct styling', async ({ platformAdminPage }) => {
        await navigateTo(platformAdminPage, page.path)
        const h1 = platformAdminPage.getByRole('heading', { level: 1 })
        await expect(h1).toBeVisible({ timeout: 10_000 })
        await expect(h1).toHaveClass(/text-3xl/)
        await expect(h1).toHaveClass(/font-bold/)
        await expect(h1).toHaveClass(/tracking-tight/)
      })

      test('has DataTable inside a Card', async ({ platformAdminPage }) => {
        await navigateTo(platformAdminPage, page.path)
        await expect(
          platformAdminPage.getByRole('heading', { level: 1 }),
        ).toBeVisible({ timeout: 10_000 })
        const card = platformAdminPage.locator('[data-slot="card"]').first()
        await expect(card).toBeVisible({ timeout: 10_000 })
        const table = card.locator('table')
        await expect(table).toBeVisible()
      })
    })
  }
})

// ─── Detail pages ──────────────────────────────────────────────────────────────

/**
 * Detail pages are tested by navigating to a known-invalid ID.
 * Even for "not found" states, the canonical layout should render:
 *   - Breadcrumbs (nav[aria-label="Breadcrumb"])
 *   - PageShell wrapper (space-y-6)
 *
 * Detail pages that render a full detail view (with tabs) require a live backend
 * and seeded data. Those structural checks are skipped unless a backend is available.
 */

const DETAIL_PAGES = [
  { path: '/accounts/test-id-000', listPath: '/accounts', label: 'Accounts' },
  { path: '/parties/test-id-000', listPath: '/parties', label: 'Parties' },
  { path: '/payments/test-id-000', listPath: '/payments', label: 'Payments' },
  { path: '/positions/test-id-000', listPath: '/positions', label: 'Positions' },
  { path: '/starlark-config/test-id-000', listPath: '/starlark-config', label: 'Starlark' },
  { path: '/gateway-mappings/test-id-000', listPath: '/gateway-mappings', label: 'Mappings' },
] as const

test.describe('Structural conformance — detail pages', () => {
  for (const page of DETAIL_PAGES) {
    test.describe(page.path, () => {
      test('has Breadcrumbs navigation', async ({ authenticatedPage }) => {
        await navigateTo(authenticatedPage, page.path)
        // Breadcrumbs component renders nav[aria-label="Breadcrumb"]
        const breadcrumbs = authenticatedPage.getByLabel('Breadcrumb')
        await expect(breadcrumbs).toBeVisible({ timeout: 10_000 })
      })

      test('has PageShell wrapper (space-y-6)', async ({ authenticatedPage }) => {
        await navigateTo(authenticatedPage, page.path)
        // Even error/not-found states should use PageShell
        const shell = authenticatedPage.locator('.space-y-6').first()
        await expect(shell).toBeVisible({ timeout: 10_000 })
      })
    })
  }
})

// ─── Reference Data hub page ───────────────────────────────────────────────────

test.describe('Structural conformance — Reference Data hub', () => {
  test('has PageShell and PageHeader', async ({ authenticatedPage }) => {
    await navigateTo(authenticatedPage, '/reference-data')
    const h1 = authenticatedPage.getByRole('heading', { level: 1 })
    await expect(h1).toBeVisible({ timeout: 10_000 })
    await expect(h1).toHaveClass(/text-3xl/)
    await expect(h1).toHaveClass(/font-bold/)
    await expect(h1).toHaveClass(/tracking-tight/)
    const shell = authenticatedPage.locator('.space-y-6').filter({
      has: authenticatedPage.getByRole('heading', { level: 1 }),
    })
    await expect(shell).toBeVisible()
  })
})
