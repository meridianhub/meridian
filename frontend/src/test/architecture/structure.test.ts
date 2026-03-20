import { describe, it, expect } from 'vitest'
import * as fs from 'fs'
import * as path from 'path'

const FEATURES_DIR = path.resolve(__dirname, '../../features')

const FEATURE_NAMES = fs
  .readdirSync(FEATURES_DIR, { withFileTypes: true })
  .filter((d) => d.isDirectory())
  .map((d) => d.name)
  .sort()

/**
 * Known cross-feature imports that predate this test.
 * Each entry: "sourceFeature -> targetFeature" (direction of the import).
 * New violations must not be added - fix coupling by extracting to shared/.
 */
const ALLOWED_CROSS_FEATURE_IMPORTS: Record<string, string[]> = {
  'accounts -> reference-data': [
    'src/features/accounts/pages/[accountId].tsx',
  ],
  'cookbook -> sagas': ['src/features/cookbook/pages/detail.tsx'],
  'economy -> manifests': [
    'src/features/economy/components/__tests__/conflict-resolution-modal.test.tsx',
    'src/features/economy/components/apply-resource-modal.tsx',
    'src/features/economy/components/conflict-resolution-modal.tsx',
    'src/features/economy/components/editor-graph-panel.tsx',
    'src/features/economy/components/manifest-diff.test.tsx',
    'src/features/economy/components/manifest-diff.tsx',
    'src/features/economy/lib/resource-schema-registry.ts',
    'src/features/economy/pages/economy-overview-page.test.tsx',
    'src/features/economy/pages/economy-overview-page.tsx',
  ],
  'manifests -> economy': [
    'src/features/manifests/components/manifest-graph.test.tsx',
    'src/features/manifests/components/manifest-graph.tsx',
  ],
  'mappings -> sagas': ['src/features/mappings/pages/[mappingId].test.tsx'],
  'payments -> accounts': [
    'src/features/payments/pages/dialogs/payment-form-utils.ts',
  ],
  'payments -> sagas': [
    'src/features/payments/pages/payment-detail-query.ts',
    'src/features/payments/pages/payment-detail.tsx',
  ],
  'reconciliation -> sagas': [
    'src/features/reconciliation/pages/detail.tsx',
  ],
  'reference-data -> manifests': [
    'src/features/reference-data/components/execution-context-tab.test.tsx',
    'src/features/reference-data/components/execution-context-tab.tsx',
    'src/features/reference-data/components/execution-subgraph.test.tsx',
    'src/features/reference-data/components/execution-subgraph.tsx',
    'src/features/reference-data/lib/filter-subgraph.ts',
  ],
  'reference-data -> sagas': [
    'src/features/reference-data/pages/account-types/create-account-type-dialog.tsx',
    'src/features/reference-data/pages/account-types/index.tsx',
    'src/features/reference-data/pages/instruments/index.tsx',
  ],
}

function getAllTsFiles(dir: string): string[] {
  const results: string[] = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      results.push(...getAllTsFiles(fullPath))
    } else if (/\.(ts|tsx)$/.test(entry.name)) {
      results.push(fullPath)
    }
  }
  return results
}

type Violation = {
  feature: string
  file: string
  importedFeature: string
  suggestion: string
}

function findCrossFeatureImports(): Violation[] {
  const violations: Violation[] = []
  const crossFeaturePattern = /@\/features\/([^/]+)\//

  for (const feature of FEATURE_NAMES) {
    const featureDir = path.join(FEATURES_DIR, feature)
    const files = getAllTsFiles(featureDir)

    for (const filePath of files) {
      const content = fs.readFileSync(filePath, 'utf-8')
      const lines = content.split('\n')

      for (const line of lines) {
        if (
          !line.match(/^\s*(import|export)\s/) &&
          !line.match(/^\s*} from\s/)
        ) {
          continue
        }

        const match = line.match(crossFeaturePattern)
        if (!match) continue

        const importedFeature = match[1]
        if (importedFeature === feature) continue

        const relativePath = path
          .relative(path.resolve(FEATURES_DIR, '../..'), filePath)
          .split(path.sep)
          .join('/')

        const key = `${feature} -> ${importedFeature}`
        const allowedFiles = ALLOWED_CROSS_FEATURE_IMPORTS[key] ?? []
        if (allowedFiles.includes(relativePath)) continue

        violations.push({
          feature,
          file: relativePath,
          importedFeature,
          suggestion: `Move shared code to shared/, api/, hooks/, contexts/, or lib/. Import from there instead of @/features/${importedFeature}/...`,
        })
      }
    }
  }

  return violations
}

describe('Frontend feature module structure', () => {
  it('every feature directory has a barrel export (index.ts or index.tsx)', () => {
    const missing: string[] = []

    for (const feature of FEATURE_NAMES) {
      const hasIndex =
        fs.existsSync(path.join(FEATURES_DIR, feature, 'index.ts')) ||
        fs.existsSync(path.join(FEATURES_DIR, feature, 'index.tsx'))
      if (!hasIndex) {
        missing.push(feature)
      }
    }

    expect(missing, `Features missing barrel export: ${missing.join(', ')}`).toEqual([])
  })

  it('features do not import directly from other features (cross-feature coupling)', () => {
    const violations = findCrossFeatureImports()

    if (violations.length > 0) {
      const report = violations
        .map(
          (v) =>
            `  ${v.feature} -> ${v.importedFeature}\n    File: ${v.file}\n    ${v.suggestion}`,
        )
        .join('\n\n')

      expect.fail(
        `Found ${violations.length} cross-feature import(s) not in allowlist:\n\n${report}\n\n` +
          'To fix: extract shared code to shared/ or add to ALLOWED_CROSS_FEATURE_IMPORTS if this is a known legacy coupling.',
      )
    }
  })

  it('allowlist does not contain stale entries', () => {
    const staleEntries: string[] = []

    for (const [key, files] of Object.entries(ALLOWED_CROSS_FEATURE_IMPORTS)) {
      for (const file of files) {
        const fullPath = path.resolve(FEATURES_DIR, '../..', file)
        if (!fs.existsSync(fullPath)) {
          staleEntries.push(`${key}: ${file}`)
        }
      }
    }

    expect(
      staleEntries,
      `Stale allowlist entries (files no longer exist):\n  ${staleEntries.join('\n  ')}`,
    ).toEqual([])
  })

  it('ratchet: allowlist must not grow (count check)', () => {
    const totalAllowed = Object.values(ALLOWED_CROSS_FEATURE_IMPORTS).reduce(
      (sum, files) => sum + files.length,
      0,
    )

    // Current count of allowed violations. This number must only decrease.
    const RATCHET_MAX = 26

    expect(
      totalAllowed,
      `Allowlist has ${totalAllowed} entries (max ${RATCHET_MAX}). ` +
        'Remove resolved entries or extract shared code to reduce coupling.',
    ).toBeLessThanOrEqual(RATCHET_MAX)
  })
})
