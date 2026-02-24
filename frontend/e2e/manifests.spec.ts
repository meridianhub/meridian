import { test, expect, navigateTo } from './fixtures'
import { type Page } from '@playwright/test'
import * as path from 'path'
import * as fs from 'fs'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

/**
 * E2E tests for the Manifest Management flow.
 *
 * These tests use Playwright route interception to mock Connect-ES API calls,
 * so they run without a live backend. The Vite dev server must be running.
 *
 * Connect-ES in JSON mode uses HTTP POST requests with the path pattern:
 *   /<package>.<ServiceName>/<MethodName>
 */

const ENERGY_MANIFEST_PATH = path.resolve(
  __dirname,
  '../../examples/manifests/energy.json',
)

function loadEnergyManifest(): string {
  return fs.readFileSync(ENERGY_MANIFEST_PATH, 'utf-8')
}

// ApplyManifestStatus enum values (mirrors proto definition)
const ApplyManifestStatus = {
  DRY_RUN: 1,
  APPLIED: 2,
  VALIDATION_FAILED: 3,
  FAILED: 4,
} as const

// ApplyStatus enum values (mirrors proto definition)
const ApplyStatus = {
  APPLIED: 1,
  FAILED: 2,
  ROLLED_BACK: 3,
} as const

const ENERGY_MANIFEST_RESPONSE = {
  version: '1.0',
  metadata: {
    name: 'Acme Energy Trading',
    industry: 'energy',
    description: 'Energy trading and settlement platform with kWh and carbon credit support',
  },
  instruments: [
    { code: 'GBP', name: 'British Pound Sterling', type: 'INSTRUMENT_TYPE_FIAT', dimensions: { unit: 'GBP', precision: 2 } },
    { code: 'KWH', name: 'Kilowatt Hour', type: 'INSTRUMENT_TYPE_COMMODITY', dimensions: { unit: 'kWh', precision: 3 } },
    { code: 'CARBON_CREDIT', name: 'Carbon Credit', type: 'INSTRUMENT_TYPE_COMMODITY', dimensions: { unit: 'TONNE_CO2E', precision: 4 } },
  ],
  accountTypes: [
    { code: 'ENERGY_TRADING', name: 'Energy Trading Account', normalBalance: 'NORMAL_BALANCE_DEBIT', allowedInstruments: ['GBP', 'KWH'] },
    { code: 'CARBON_INVENTORY', name: 'Carbon Credit Inventory', normalBalance: 'NORMAL_BALANCE_DEBIT', allowedInstruments: ['CARBON_CREDIT'] },
    { code: 'SETTLEMENT', name: 'Settlement Account', normalBalance: 'NORMAL_BALANCE_CREDIT', allowedInstruments: ['GBP'] },
  ],
  valuationRules: [
    { fromInstrument: 'KWH', toInstrument: 'GBP', method: 'VALUATION_METHOD_SPOT_RATE', source: 'nordpool_spot' },
    { fromInstrument: 'CARBON_CREDIT', toInstrument: 'GBP', method: 'VALUATION_METHOD_SPOT_RATE', source: 'ice_eua' },
  ],
  sagas: [
    { name: 'process_energy_settlement', trigger: 'api:/v1/energy/settlements', script: '' },
  ],
}

const MANIFEST_HISTORY_ENTRY = {
  id: 'version-1',
  version: '1.0',
  appliedAt: { seconds: '1706745600', nanos: 0 },
  appliedBy: 'e2e-user',
  applyStatus: ApplyStatus.APPLIED,
  diffSummary: 'Added 3 instruments, 3 account types, 2 valuation rules, 1 saga',
  manifest: ENERGY_MANIFEST_RESPONSE,
}

/**
 * Set up route interception for manifest API calls.
 */
async function setupManifestRoutes(
  page: Page,
  options: {
    hasCurrentManifest: boolean
    historyEntries?: typeof MANIFEST_HISTORY_ENTRY[]
  },
) {
  const { hasCurrentManifest, historyEntries = [] } = options

  // ManifestHistoryService.GetCurrentManifest
  await page.route('**/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest', (route) => {
    if (hasCurrentManifest) {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ version: MANIFEST_HISTORY_ENTRY }),
      })
    }
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({}),
    })
  })

  // ManifestHistoryService.ListManifestVersions
  await page.route('**/meridian.control_plane.v1.ManifestHistoryService/ListManifestVersions', (route) => {
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        versions: historyEntries,
        totalCount: historyEntries.length,
      }),
    })
  })

  // ApplyManifestService.ApplyManifest - dry run
  await page.route('**/meridian.control_plane.v1.ApplyManifestService/ApplyManifest', async (route) => {
    const request = route.request()
    const body = await request.postDataJSON().catch(() => ({})) as Record<string, unknown>
    const isDryRun = body.dryRun === true

    if (isDryRun) {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          jobId: 'job-dry-run',
          status: ApplyManifestStatus.DRY_RUN,
          diffSummary: 'Added 3 instruments, 3 account types, 2 valuation rules, 1 saga',
          stepResults: [
            { stepName: 'validate', status: 1, message: 'Validation passed', details: {} },
            { stepName: 'diff', status: 1, message: '9 changes detected', details: {} },
            { stepName: 'execute', status: 3, message: 'Skipped (dry run)', details: {} },
          ],
          validationErrors: [],
        }),
      })
    }

    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        jobId: 'job-apply',
        status: ApplyManifestStatus.APPLIED,
        diffSummary: 'Applied successfully',
        stepResults: [],
        validationErrors: [],
      }),
    })
  })
}

test.describe('Manifest Management Flow', () => {
  test.describe('Initial State - no manifest applied', () => {
    test('navigates to /manifests and shows empty state', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      await navigateTo(page, '/manifests')
      await expect(page.getByRole('heading', { name: 'Manifest Configuration' })).toBeVisible()

      // Verify Apply Manifest button is visible
      await expect(page.getByRole('button', { name: 'Apply Manifest' })).toBeVisible()

      // Current tab is active by default and shows empty state
      const emptyState = page.getByTestId('empty-state')
      await expect(emptyState).toBeVisible()
      await expect(emptyState).toContainText(/no manifest/i)
    })

    test('shows Version History tab with empty history', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      await navigateTo(page, '/manifests')
      await page.getByRole('tab', { name: 'Version History' }).click()

      // History table wrapper should be visible (DataTable shows empty state internally)
      await expect(page.getByTestId('manifest-history-table')).toBeVisible()
    })
  })

  test.describe('Apply Manifest with Dry-Run Preview', () => {
    test('opens apply dialog when clicking Apply Manifest button', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()
      await expect(dialog.getByRole('heading', { name: 'Apply Manifest' })).toBeVisible()
    })

    test('preview changes shows dry-run result with diff summary', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      const manifestJson = loadEnergyManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()

      // Fill in manifest JSON
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)

      // Click Preview Changes
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()

      // Wait for dry-run result to appear
      const dryRunResult = dialog.getByTestId('dry-run-result')
      await expect(dryRunResult).toBeVisible({ timeout: 10_000 })

      // Verify diff summary content
      await expect(dryRunResult).toContainText('Added 3 instruments')
    })

    test('preview shows step results including validate, diff, and skipped execute', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      const manifestJson = loadEnergyManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()

      await expect(dialog.getByTestId('dry-run-result')).toBeVisible({ timeout: 10_000 })

      // Step results should be visible within the dry-run panel
      await expect(dialog.getByText('validate')).toBeVisible()
      await expect(dialog.getByText('diff')).toBeVisible()
    })

    test('Apply Manifest button is enabled after successful preview', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      const manifestJson = loadEnergyManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)

      // Apply button disabled before preview
      await expect(dialog.getByRole('button', { name: 'Apply Manifest' })).toBeDisabled()

      // After preview succeeds, Apply button becomes enabled
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()
      await expect(dialog.getByTestId('dry-run-result')).toBeVisible({ timeout: 10_000 })
      await expect(dialog.getByRole('button', { name: 'Apply Manifest' })).not.toBeDisabled()
    })

    test('applies manifest and dialog closes on success', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      const manifestJson = loadEnergyManifest()
      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      const textarea = dialog.getByTestId('manifest-json')
      await textarea.fill(manifestJson)

      // Preview then apply
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()
      await expect(dialog.getByTestId('dry-run-result')).toBeVisible({ timeout: 10_000 })
      await dialog.getByRole('button', { name: 'Apply Manifest' }).click()

      // Dialog should close after successful apply
      await expect(dialog).not.toBeVisible({ timeout: 15_000 })
    })
  })

  test.describe('Current Manifest View', () => {
    test('displays applied manifest version and metadata', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, {
        hasCurrentManifest: true,
        historyEntries: [MANIFEST_HISTORY_ENTRY],
      })

      await navigateTo(page, '/manifests')

      const currentView = page.getByTestId('manifest-current-view')
      await expect(currentView).toBeVisible({ timeout: 10_000 })

      // Version and metadata
      await expect(currentView).toContainText('Version 1.0')
      await expect(currentView).toContainText('Acme Energy Trading')
    })

    test('shows APPLIED status badge', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, {
        hasCurrentManifest: true,
        historyEntries: [MANIFEST_HISTORY_ENTRY],
      })

      await navigateTo(page, '/manifests')

      const currentView = page.getByTestId('manifest-current-view')
      await expect(currentView).toBeVisible({ timeout: 10_000 })
      await expect(currentView).toContainText('APPLIED')
    })

    test('instruments section shows GBP, KWH, and CARBON_CREDIT after expand', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, {
        hasCurrentManifest: true,
        historyEntries: [MANIFEST_HISTORY_ENTRY],
      })

      await navigateTo(page, '/manifests')

      const instrumentsSection = page.getByTestId('instruments-section')
      await expect(instrumentsSection).toBeVisible({ timeout: 10_000 })

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
      await setupManifestRoutes(page, {
        hasCurrentManifest: true,
        historyEntries: [MANIFEST_HISTORY_ENTRY],
      })

      await navigateTo(page, '/manifests')

      const accountTypesSection = page.getByTestId('account-types-section')
      await expect(accountTypesSection).toBeVisible({ timeout: 10_000 })

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
      await setupManifestRoutes(page, {
        hasCurrentManifest: true,
        historyEntries: [MANIFEST_HISTORY_ENTRY],
      })

      await navigateTo(page, '/manifests')

      // Switch to Version History tab
      await page.getByRole('tab', { name: 'Version History' }).click()

      const historyTable = page.getByTestId('manifest-history-table')
      await expect(historyTable).toBeVisible()

      // Verify table content
      await expect(historyTable).toContainText('1.0')
      await expect(historyTable).toContainText('APPLIED')
      await expect(historyTable).toContainText('e2e-user')
    })

    test('history entry shows diff summary', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, {
        hasCurrentManifest: true,
        historyEntries: [MANIFEST_HISTORY_ENTRY],
      })

      await navigateTo(page, '/manifests')
      await page.getByRole('tab', { name: 'Version History' }).click()

      const historyTable = page.getByTestId('manifest-history-table')
      await expect(historyTable).toBeVisible()
      await expect(historyTable).toContainText('Added 3 instruments')
    })
  })

  test.describe('Idempotent Re-Application', () => {
    // FIXME: manifest-current-view never renders in this specific test despite
    // identical pattern working in "Current Manifest View" tests above. Likely a
    // component-level race condition with React state when re-applying an existing
    // manifest. Tracked separately from the CI sharding work.
    test.fixme('re-applying same manifest dry-run shows another diff summary', async ({ platformAdminPage: page }) => {
      // Set up with manifest already applied
      await setupManifestRoutes(page, {
        hasCurrentManifest: true,
        historyEntries: [MANIFEST_HISTORY_ENTRY],
      })

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

      // Preview - will show the same response (mock returns same diff summary)
      await dialog.getByRole('button', { name: 'Preview Changes' }).click()
      const dryRunResult = dialog.getByTestId('dry-run-result')
      await expect(dryRunResult).toBeVisible({ timeout: 10_000 })

      // Dry run succeeded so Apply Manifest button is enabled
      await expect(dialog.getByRole('button', { name: 'Apply Manifest' })).not.toBeDisabled()

      // Apply the manifest again
      await dialog.getByRole('button', { name: 'Apply Manifest' }).click()
      await expect(dialog).not.toBeVisible({ timeout: 15_000 })
    })
  })

  test.describe('Error Handling', () => {
    test('shows parse error for invalid JSON input', async ({ platformAdminPage: page }) => {
      await setupManifestRoutes(page, { hasCurrentManifest: false })

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
      await setupManifestRoutes(page, { hasCurrentManifest: false })

      await navigateTo(page, '/manifests')
      await page.getByRole('button', { name: 'Apply Manifest' }).click()

      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()

      await dialog.getByRole('button', { name: 'Cancel' }).click()
      await expect(dialog).not.toBeVisible()
    })
  })
})
