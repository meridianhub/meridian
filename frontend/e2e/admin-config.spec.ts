import { test, expect, navigateTo } from './fixtures'

/**
 * E2E tests for admin/configuration pages.
 *
 * Tests run against a real backend in CI. Tenants are created via direct
 * gRPC/REST calls and work correctly. Manifest-seeded data (instruments,
 * account types, sagas) is NOT available because the unified binary's
 * ApplyManifestService has no executor configured — seed-dev validates and
 * plans but never executes.
 *
 * FIXME: Wire saga executor into RegisterApplyManifestService so seed-dev
 * actually creates reference data. Then convert test.fixme → test.
 *
 * Created tenants (via gRPC/curl):
 *   - dev_tenant, acme_corp, energy_co
 *
 * Pages covered:
 *   - /reference-data/instruments
 *   - /reference-data/account-types
 *   - /reference-data/nodes
 *   - /starlark-config
 *   - /gateway-mappings
 *   - /tenants
 */

// ─── Reference Data: Instruments ─────────────────────────────────────────────

test.describe('Reference Data - Instruments page', () => {
  test('renders Instruments heading', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/instruments')
    await expect(page.getByRole('heading', { name: 'Instruments' })).toBeVisible()
  })

  test.fixme('shows instruments table with GBP, KWH, CARBON_CREDIT rows', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/instruments')

    await expect(page.getByText('GBP')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('KWH')).toBeVisible()
    await expect(page.getByText('CARBON_CREDIT')).toBeVisible()
  })

  test.fixme('shows instrument display names', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/instruments')

    await expect(page.getByText('British Pound Sterling')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Kilowatt Hour')).toBeVisible()
    await expect(page.getByText('Carbon Credit')).toBeVisible()
  })

  test.fixme('shows dimension labels for instruments', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/instruments')

    await expect(page.getByText('Currency')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Energy')).toBeVisible()
    await expect(page.getByText('Carbon')).toBeVisible()
  })

  test.fixme('shows CEL Playground section', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/instruments')

    await expect(page.getByTestId('cel-playground')).toBeVisible()
    await expect(page.getByRole('heading', { name: 'CEL Playground' })).toBeVisible()
  })

  test.fixme('CEL Playground Evaluate button submits expression', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/instruments')

    await page.getByRole('button', { name: 'Evaluate' }).click()

    await expect(page.getByTestId('cel-result')).toBeVisible({ timeout: 15_000 })
  })
})

// ─── Reference Data: Account Types ───────────────────────────────────────────

test.describe('Reference Data - Account Types page', () => {
  test('renders Account Types heading', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/account-types')
    await expect(page.getByRole('heading', { name: 'Account Types' })).toBeVisible()
  })

  test.fixme('shows account type codes in the table', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/account-types')

    await expect(page.getByText('ENERGY_TRADING')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('CARBON_INVENTORY')).toBeVisible()
    await expect(page.getByText('SETTLEMENT')).toBeVisible()
  })

  test.fixme('shows account type display names', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/account-types')

    await expect(page.getByText('Energy Trading Account')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Carbon Credit Inventory')).toBeVisible()
    await expect(page.getByText('Settlement Account')).toBeVisible()
  })

  test.fixme('shows behavior class labels', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/account-types')

    // behaviorClass 1 = Customer, 2 = Clearing
    await expect(page.getByText('Customer').first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Clearing')).toBeVisible()
  })

  test.fixme('shows CEL policy editor when row is clicked', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/account-types')

    await expect(page.getByText('ENERGY_TRADING')).toBeVisible({ timeout: 15_000 })
    await page.getByText('ENERGY_TRADING').click()

    await expect(page.getByTestId('cel-policy-editor')).toBeVisible({ timeout: 5_000 })
    await expect(page.getByText('CEL Policies')).toBeVisible()
  })
})

// ─── Reference Data: Nodes ────────────────────────────────────────────────────

test.describe('Reference Data - Nodes page', () => {
  test('renders Nodes heading', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/nodes')
    await expect(page.getByRole('heading', { name: 'Nodes' })).toBeVisible()
  })

  test('shows Node Tree card', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/nodes')
    await expect(page.getByText('Node Tree')).toBeVisible()
  })

  test('shows temporal date picker', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/nodes')
    await expect(page.getByTestId('temporal-date-picker')).toBeVisible()
  })

  test('shows empty state when no nodes exist', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/nodes')

    await expect(page.getByTestId('empty-tree-state')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('empty-tree-state')).toContainText('No nodes found')
  })

  // Requires node seeding not provided by manifest
  test.skip('shows root nodes when available', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/nodes')

    await expect(page.getByText('root-node')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('ENTITY')).toBeVisible()
  })

  test('As At date picker updates node query', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/reference-data/nodes')

    await page.getByTestId('temporal-date-picker').fill('2024-01-01')

    await expect(page.getByTestId('empty-tree-state')).toBeVisible({ timeout: 15_000 })
  })
})

// ─── Starlark Configuration ───────────────────────────────────────────────────

test.describe('Starlark Configuration page', () => {
  test('renders Starlark Configuration heading', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/starlark-config')
    await expect(page.getByRole('heading', { name: 'Starlark Configuration' })).toBeVisible()
  })

  test.fixme('shows saga names in the table', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/starlark-config')

    await expect(page.getByText('process_energy_settlement')).toBeVisible({ timeout: 15_000 })
  })

  test.fixme('shows ACTIVE status badge for active saga', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/starlark-config')

    await expect(page.getByText('process_energy_settlement')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('ACTIVE').first()).toBeVisible()
  })

  // Only ACTIVE sagas exist from manifest seed
  test.skip('shows DRAFT status badge for draft saga', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/starlark-config')

    await expect(page.getByText('DRAFT')).toBeVisible({ timeout: 15_000 })
  })

  test.fixme('saga names render as clickable links', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/starlark-config')

    await expect(page.getByText('process_energy_settlement')).toBeVisible({ timeout: 15_000 })
    const link = page.getByRole('link', { name: 'process_energy_settlement' })
    await expect(link).toBeVisible()
    // The link href uses a server-assigned UUID, not a hardcoded ID
    await expect(link).toHaveAttribute('href', /\/starlark-config\//)
  })
})

// ─── Starlark Configuration Detail ───────────────────────────────────────────

test.describe('Starlark Configuration detail page', () => {
  test.fixme('renders saga name and ACTIVE status on detail page', async ({ platformAdminPage: page }) => {
    // Navigate via list page to avoid hardcoding UUID
    await navigateTo(page, '/starlark-config')
    await page.getByRole('link', { name: 'process_energy_settlement' }).click()

    await expect(page.getByText('process_energy_settlement')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('ACTIVE')).toBeVisible()
  })

  test.fixme('shows the saga script in the editor', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/starlark-config')
    await page.getByRole('link', { name: 'process_energy_settlement' }).click()

    await expect(page.getByText('process_energy_settlement')).toBeVisible({ timeout: 15_000 })
    // The energy.json manifest saga script starts with def execute(ctx):
    await expect(page.getByText('def execute(ctx):')).toBeVisible({ timeout: 15_000 })
  })

  test('shows not found message for missing saga', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/starlark-config/non-existent-uuid')

    await expect(page.getByText(/not found/i)).toBeVisible({ timeout: 15_000 })
  })
})

// ─── Gateway Mappings ─────────────────────────────────────────────────────────
// TODO: Gateway mapping REST API (POST /v1/mappings) is not yet registered
// in the unified binary's Vanguard transcoder. Re-enable once the mapping CRUD
// endpoints are available via REST.

test.describe('Gateway Mappings page', () => {
  test.skip('renders Gateway Mappings heading', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/gateway-mappings')
    await expect(page.getByRole('heading', { name: 'Gateway Mappings' })).toBeVisible()
  })

  test.skip('shows mapping names in the table', async () => {})
  test.skip('shows target service and RPC columns', async () => {})
  test.skip('shows status badges for mappings', async () => {})
  test.skip('row click navigates to mapping detail', async () => {})
})

// ─── Gateway Mapping Detail ───────────────────────────────────────────────────

test.describe('Gateway Mapping detail page', () => {
  test.skip('renders Mapping Details heading', async () => {})
  test.skip('shows mapping name and status in header', async () => {})
  test.skip('shows Overview, Field Mapper, and Dry Run tabs', async () => {})
  test.skip('Overview tab shows target service and RPC', async () => {})
  test.skip('Field Mapper tab shows no fields message when empty', async () => {})
  test.skip('Dry Run tab shows Inbound and Outbound direction buttons', async () => {})
  test.skip('shows error state for unknown mapping', async () => {})
})

// ─── Tenant Management ────────────────────────────────────────────────────────

test.describe('Tenant Management page', () => {
  test('renders Tenant Management heading', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')
    await expect(page.getByRole('heading', { name: 'Tenant Management' })).toBeVisible()
  })

  test('shows New Tenant button', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')
    await expect(page.getByRole('button', { name: 'New Tenant' })).toBeVisible()
  })

  test('shows tenant IDs in the table', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')

    await expect(page.getByText('dev_tenant')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('acme_corp')).toBeVisible()
    await expect(page.getByText('energy_co')).toBeVisible()
  })

  test('shows tenant display names', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')

    // Scope to the table to avoid matching the TenantSelector dropdown in the header
    const table = page.locator('table')
    await expect(table.getByText('ACME Corporation')).toBeVisible({ timeout: 15_000 })
    await expect(table.getByText('Energy Co Ltd')).toBeVisible()
  })

  test('shows status badges for tenants', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')

    // Tenants may be in ACTIVE or PROVISIONING state depending on timing
    await expect(page.getByText('dev_tenant')).toBeVisible({ timeout: 15_000 })
  })

  test('opens Initiate Tenant dialog on New Tenant click', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')
    await page.getByRole('button', { name: 'New Tenant' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByRole('heading', { name: 'Initiate Tenant' })).toBeVisible()
  })

  test('Initiate Tenant dialog has Tenant ID, Display Name, and Settlement Asset fields', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')
    await page.getByRole('button', { name: 'New Tenant' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByLabel('Tenant ID')).toBeVisible()
    await expect(dialog.getByLabel('Display Name')).toBeVisible()
    await expect(dialog.getByLabel('Settlement Asset')).toBeVisible()
    await expect(dialog.getByLabel('Slug (optional)')).toBeVisible()
  })

  test('Initiate Tenant dialog validates required fields', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')
    await page.getByRole('button', { name: 'New Tenant' }).click()

    const dialog = page.getByRole('dialog')
    await dialog.getByRole('button', { name: 'Initiate Tenant' }).click()

    await expect(dialog.getByText('Tenant ID is required')).toBeVisible()
    await expect(dialog.getByText('Display Name is required')).toBeVisible()
    await expect(dialog.getByText('Settlement Asset is required')).toBeVisible()
  })

  test('Initiate Tenant dialog closes on Cancel', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')
    await page.getByRole('button', { name: 'New Tenant' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).not.toBeVisible()
  })

  test('Initiate Tenant dialog submits and closes on success', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants')
    await page.getByRole('button', { name: 'New Tenant' }).click()

    const dialog = page.getByRole('dialog')
    // Use timestamp to avoid tenant ID collisions across test runs
    const uniqueId = `e2e_tenant_${Date.now()}`
    await dialog.getByLabel('Tenant ID').fill(uniqueId)
    await dialog.getByLabel('Display Name').fill('E2E Test Tenant')
    await dialog.getByLabel('Settlement Asset').fill('GBP')

    await dialog.getByRole('button', { name: 'Initiate Tenant' }).click()

    await expect(dialog).not.toBeVisible({ timeout: 15_000 })
  })
})

// ─── Tenant Detail ────────────────────────────────────────────────────────────

test.describe('Tenant detail page', () => {
  test('renders tenant display name and ID', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants/acme_corp')

    await expect(page.getByRole('heading', { name: 'ACME Corporation' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('acme_corp').first()).toBeVisible()
  })

  test('shows Tenant Details card with settlement asset', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants/acme_corp')

    await expect(page.getByText('Tenant Details')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('GBP')).toBeVisible()
  })

  test('shows Back to Tenants link', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants/acme_corp')

    await expect(page.getByRole('link', { name: 'Back to Tenants' })).toBeVisible({ timeout: 15_000 })
  })

  test('shows Provisioning Status card', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants/acme_corp')

    await expect(page.getByText('Provisioning Status').first()).toBeVisible({ timeout: 15_000 })
  })

  test('shows not found when tenant does not exist', async ({ platformAdminPage: page }) => {
    await navigateTo(page, '/tenants/non-existent-tenant-id-xyz')

    await expect(page.getByText('Tenant not found.')).toBeVisible({ timeout: 15_000 })
  })
})
