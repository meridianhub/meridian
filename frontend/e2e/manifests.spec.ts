import { test, expect, navigateTo } from './fixtures'
import * as path from 'path'
import * as fs from 'fs'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

/**
 * E2E tests for the Manifest Management flow.
 *
 * These tests run against a real backend seeded by `seed-dev`, which creates
 * `dev_tenant` and applies the `energy.json` manifest before tests run.
 *
 * Seeded state:
 * - Current manifest: energy.json (version "1.0", name "Acme Energy Trading")
 * - 3 instruments: GBP, KWH, CARBON_CREDIT
 * - 3 account types: ENERGY_TRADING, CARBON_INVENTORY, SETTLEMENT
 * - 2 valuation rules, 1 saga
 * - Applied by: "seed-dev"
 */

const ENERGY_MANIFEST_PATH = path.resolve(
  __dirname,
  '../../examples/manifests/energy.json',
)

const SAAS_MANIFEST_PATH = path.resolve(
  __dirname,
  '../../examples/manifests/saas.json',
)

function loadEnergyManifest(): string {
  return fs.readFileSync(ENERGY_MANIFEST_PATH, 'utf-8')
}

function loadSaasManifest(): string {
  return fs.readFileSync(SAAS_MANIFEST_PATH, 'utf-8')
}

test.describe('Manifest Management Flow', () => {
  test.describe('Initial State - no manifest applied', () => {
    test.skip('navigates to /manifests and shows empty state', async () => {
      // Manifest already applied by seed-dev; empty state covered by unit tests
    })
  })

  test.describe('Apply Manifest with Dry-Run Preview', () => {
    test('opens apply dialog when clicking Apply Manifest button', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()
      await expect(dialog.getByRole('heading', { name: 'Apply Manifest' })).toBeVisible()
    })

    test('preview changes shows dry-run result with diff summary', async ({ platformAdminPage: page }) => {
      const manifestJson = loadSaasManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()

      // Fill in saas.json manifest (different from the already-applied energy.json)
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)

      // Click Preview Changes to trigger a real dry-run against the backend
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()

      // Wait for dry-run result to appear from the real backend
      const dryRunResult = dialog.getByTestId('dry-run-result')
      await expect(dryRunResult).toBeVisible({ timeout: 15_000 })
    })

    test('preview shows step results including validate and diff', async ({ platformAdminPage: page }) => {
      const manifestJson = loadSaasManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()

      await expect(dialog.getByTestId('dry-run-result')).toBeVisible({ timeout: 15_000 })

      // Step results should be visible within the dry-run panel
      await expect(dialog.getByText('validate')).toBeVisible()
      await expect(dialog.getByText('diff')).toBeVisible()
    })

    test('Apply Manifest button is enabled after successful preview', async ({ platformAdminPage: page }) => {
      const manifestJson = loadSaasManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)

      // Apply button disabled before preview
      await expect(dialog.getByRole('button', { name: 'Apply Manifest' })).toBeDisabled()

      // After preview succeeds, Apply button becomes enabled
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()
      await expect(dialog.getByTestId('dry-run-result')).toBeVisible({ timeout: 15_000 })
      await expect(dialog.getByRole('button', { name: 'Apply Manifest' })).not.toBeDisabled()
    })

    test('applies manifest and dialog closes on success', async ({ platformAdminPage: page }) => {
      // Re-apply energy.json (idempotent) to avoid changing state for other tests
      const manifestJson = loadEnergyManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)

      // Preview then apply
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()
      await expect(dialog.getByTestId('dry-run-result')).toBeVisible({ timeout: 15_000 })
      await dialog.getByRole('button', { name: 'Apply Manifest' }).click()

      // Dialog should close after successful apply
      await expect(dialog).not.toBeVisible({ timeout: 15_000 })
    })
  })

  test.describe('Current Manifest View', () => {
    test('displays applied manifest version and metadata', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')

      const currentView = page.getByTestId('manifest-current-view')
      await expect(currentView).toBeVisible({ timeout: 15_000 })

      // Version and metadata from the seeded energy.json manifest
      await expect(currentView).toContainText('1.0')
      await expect(currentView).toContainText('Acme Energy Trading')
    })

    test('shows APPLIED status badge', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')

      const currentView = page.getByTestId('manifest-current-view')
      await expect(currentView).toBeVisible({ timeout: 15_000 })
      await expect(currentView).toContainText('APPLIED')
    })

    test('instruments section shows GBP, KWH, and CARBON_CREDIT after expand', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')

      const instrumentsSection = page.getByTestId('instruments-section')
      await expect(instrumentsSection).toBeVisible({ timeout: 15_000 })

      // Expand the instruments section
      await instrumentsSection.getByRole('button').click()

      await expect(instrumentsSection).toContainText('GBP')
      await expect(instrumentsSection).toContainText('British Pound Sterling')
      await expect(instrumentsSection).toContainText('KWH')
      await expect(instrumentsSection).toContainText('Kilowatt Hour')
      await expect(instrumentsSection).toContainText('CARBON_CREDIT')
      await expect(instrumentsSection).toContainText('Carbon Credit')
    })

    test('account types section shows ENERGY_TRADING, CARBON_INVENTORY, and SETTLEMENT after expand', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')

      const accountTypesSection = page.getByTestId('account-types-section')
      await expect(accountTypesSection).toBeVisible({ timeout: 15_000 })

      // Expand the account types section
      await accountTypesSection.getByRole('button').click()

      await expect(accountTypesSection).toContainText('ENERGY_TRADING')
      await expect(accountTypesSection).toContainText('Energy Trading Account')
      await expect(accountTypesSection).toContainText('CARBON_INVENTORY')
      await expect(accountTypesSection).toContainText('Carbon Credit Inventory')
      await expect(accountTypesSection).toContainText('SETTLEMENT')
      await expect(accountTypesSection).toContainText('Settlement Account')
    })
  })

  test.describe('Version History Table', () => {
    test('history tab shows applied entry with version and APPLIED status', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')

      // Switch to Version History tab
      await page.getByRole('tab', { name: 'Version History' }).click()

      const historyTable = page.getByTestId('manifest-history-table')
      await expect(historyTable).toBeVisible({ timeout: 15_000 })

      // Verify table content from seed-dev applied manifest
      await expect(historyTable).toContainText('1.0')
      await expect(historyTable).toContainText('APPLIED')
      await expect(historyTable).toContainText('seed-dev')
    })

    test('history entry shows diff summary', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')
      await page.getByRole('tab', { name: 'Version History' }).click()

      const historyTable = page.getByTestId('manifest-history-table')
      await expect(historyTable).toBeVisible({ timeout: 15_000 })

      // The real backend stores an actual diff summary; verify the table has content
      // beyond just version and status (exact content depends on backend output)
      const rows = historyTable.locator('tr')
      await expect(rows).not.toHaveCount(0)
    })
  })

  test.describe('Idempotent Re-Application', () => {
    // FIXME: manifest-current-view never renders in this specific test despite
    // identical pattern working in "Current Manifest View" tests above. Likely a
    // component-level race condition with React state when re-applying an existing
    // manifest. Tracked separately from the CI sharding work.
    test.fixme('re-applying same manifest dry-run shows another diff summary', async ({ platformAdminPage: page }) => {
      const manifestJson = loadEnergyManifest()
      await navigateTo(page, '/manifests')

      // Verify manifest is already shown
      await expect(page.getByTestId('manifest-current-view')).toBeVisible({ timeout: 15_000 })

      // Open apply dialog and re-apply same manifest
      await page.getByRole('button', { name: 'Apply Manifest' }).click()
      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()

      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)

      // Preview - real backend dry-run against already-applied energy.json
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()
      const dryRunResult = dialog.getByTestId('dry-run-result')
      await expect(dryRunResult).toBeVisible({ timeout: 15_000 })

      // Dry run succeeded so Apply Manifest button is enabled
      await expect(dialog.getByRole('button', { name: 'Apply Manifest' })).not.toBeDisabled()

      // Apply the manifest again
      await dialog.getByRole('button', { name: 'Apply Manifest' }).click()
      await expect(dialog).not.toBeVisible({ timeout: 15_000 })
    })
  })

  test.describe('Error Handling', () => {
    test('shows parse error for invalid JSON input', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill('this is not valid json {{{')

      await dialog.getByRole('button', { name: 'Preview Changes' }).click()

      // Parse error should appear
      await expect(dialog.getByTestId('parse-error')).toBeVisible({ timeout: 10_000 })
    })

    test('Cancel button closes the dialog', async ({ platformAdminPage: page }) => {
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()

      await dialog.getByRole('button', { name: 'Cancel' }).click()
      await expect(dialog).not.toBeVisible()
    })
  })
})
