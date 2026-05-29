/**
 * Frontend architecture test: import boundary enforcement.
 *
 * Validates three layering rules:
 *   1. features/X must NOT import from features/Y (cross-feature coupling)
 *   2. shared/ must NOT import from features/ (dependency inversion)
 *   3. api/ must NOT import from features/ or shared/ (API layer independence)
 *
 * Test files (*.test.*, *.spec.*) and generated files (api/gen/) are excluded.
 * Uses a ratchet allowlist so existing violations are tracked and new ones are prevented.
 */
import { describe, it, expect } from 'vitest'
import * as fs from 'node:fs'
import * as path from 'node:path'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Violation {
  file: string
  line: number
  importPath: string
  boundary: 'cross-feature' | 'shared-imports-feature' | 'api-imports-feature' | 'api-imports-shared'
  suggestion: string
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SRC_DIR = path.resolve(__dirname, '../..')

/** Recursively collect .ts/.tsx files, excluding test and generated files. */
function collectSourceFiles(dir: string): string[] {
  const results: string[] = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name === 'node_modules' || entry.name === 'gen') continue
      results.push(...collectSourceFiles(full))
    } else if (/\.(ts|tsx)$/.test(entry.name)) {
      if (/\.(test|spec)\.(ts|tsx)$/.test(entry.name)) continue
      results.push(full)
    }
  }
  return results
}

/** 1-based line number for a character offset in content. */
function lineAt(content: string, offset: number): number {
  return content.substring(0, offset).split('\n').length
}

/** Extract import paths from a source file. Handles static, dynamic, and multi-line imports. */
function extractImports(content: string): { line: number; importPath: string }[] {
  const imports: { line: number; importPath: string }[] = []

  // Use regex on full content to handle multi-line imports.
  // Match: import ... from '...' (including multi-line with { } spanning lines)
  const importRegex = /import\s+(?:type\s+)?(?:[\s\S]*?)\s+from\s+['"]([^'"]+)['"]/g
  let match: RegExpExecArray | null
  while ((match = importRegex.exec(content)) !== null) {
    const importPath = match[1]
    // Line number = count newlines before the `from '...'` part
    const fromIndex = content.lastIndexOf(importPath, match.index + match[0].length)
    const lineNumber = content.substring(0, fromIndex).split('\n').length
    imports.push({ line: lineNumber, importPath })
  }

  // Side-effect imports: import '...' or import "..."
  const sideEffectRegex = /import\s+['"]([^'"]+)['"]/g
  while ((match = sideEffectRegex.exec(content)) !== null) {
    const importPath = match[1]
    // Skip if already captured by the from-style regex
    if (imports.some((i) => i.importPath === importPath)) continue
    const lineNumber = lineAt(content, match.index)
    imports.push({ line: lineNumber, importPath })
  }

  // Dynamic imports: import('...')
  const dynamicRegex = /import\s*\(\s*['"]([^'"]+)['"]\s*\)/g
  while ((match = dynamicRegex.exec(content)) !== null) {
    const lineNumber = lineAt(content, match.index)
    imports.push({ line: lineNumber, importPath: match[1] })
  }

  // Re-exports: export { X } from '...' or export * from '...'
  const reExportRegex = /export\s+(?:type\s+)?(?:\{[\s\S]*?\}|\*)\s+from\s+['"]([^'"]+)['"]/g
  while ((match = reExportRegex.exec(content)) !== null) {
    const lineNumber = lineAt(content, match.index)
    imports.push({ line: lineNumber, importPath: match[1] })
  }

  return imports
}

/**
 * Resolve an import path to a relative path from src/.
 * Handles @/ alias (maps to src/).
 * Returns null for external packages.
 */
function resolveImportPath(importPath: string): string | null {
  if (importPath.startsWith('@/')) {
    return importPath.slice(2) // strip @/ prefix
  }
  // Relative imports would need resolution relative to the file, but for boundary
  // checks we only care about @/ prefixed paths since cross-boundary imports use them.
  if (importPath.startsWith('.')) {
    return null // relative imports within same feature are fine
  }
  return null // external package
}

/** Extract the feature name from a resolved path like "features/accounts/..." */
function extractFeatureName(resolvedPath: string): string | null {
  const match = resolvedPath.match(/^features\/([^/]+)/)
  return match ? match[1] : null
}

/** Check all files and return violations. */
function findViolations(): Violation[] {
  const violations: Violation[] = []

  // 1. Cross-feature imports in features/
  const featuresDir = path.join(SRC_DIR, 'features')
  if (fs.existsSync(featuresDir)) {
    const featureFiles = collectSourceFiles(featuresDir)
    for (const file of featureFiles) {
      const relFile = path.relative(SRC_DIR, file).split(path.sep).join('/')
      const fileFeature = extractFeatureName(relFile)
      if (!fileFeature) continue

      const content = fs.readFileSync(file, 'utf-8')
      for (const { line, importPath } of extractImports(content)) {
        const resolved = resolveImportPath(importPath)
        if (!resolved) continue
        const importFeature = extractFeatureName(resolved)
        if (importFeature && importFeature !== fileFeature) {
          violations.push({
            file: relFile,
            line,
            importPath,
            boundary: 'cross-feature',
            suggestion: `Move shared code to shared/ or create a shared module. "${relFile}" (feature "${fileFeature}") should not import from feature "${importFeature}".`,
          })
        }
      }
    }
  }

  // 2. shared/ must not import from features/
  const sharedDir = path.join(SRC_DIR, 'shared')
  if (fs.existsSync(sharedDir)) {
    const sharedFiles = collectSourceFiles(sharedDir)
    for (const file of sharedFiles) {
      const relFile = path.relative(SRC_DIR, file).split(path.sep).join('/')
      const content = fs.readFileSync(file, 'utf-8')
      for (const { line, importPath } of extractImports(content)) {
        const resolved = resolveImportPath(importPath)
        if (!resolved) continue
        if (resolved.startsWith('features/')) {
          violations.push({
            file: relFile,
            line,
            importPath,
            boundary: 'shared-imports-feature',
            suggestion: `shared/ must not depend on features/. Move the needed code into shared/ or restructure.`,
          })
        }
      }
    }
  }

  // 3. api/ must not import from features/ or shared/
  const apiDir = path.join(SRC_DIR, 'api')
  if (fs.existsSync(apiDir)) {
    const apiFiles = collectSourceFiles(apiDir)
    for (const file of apiFiles) {
      const relFile = path.relative(SRC_DIR, file).split(path.sep).join('/')
      const content = fs.readFileSync(file, 'utf-8')
      for (const { line, importPath } of extractImports(content)) {
        const resolved = resolveImportPath(importPath)
        if (!resolved) continue
        if (resolved.startsWith('features/')) {
          violations.push({
            file: relFile,
            line,
            importPath,
            boundary: 'api-imports-feature',
            suggestion: `api/ must not depend on features/. The API layer should be independent.`,
          })
        }
        if (resolved.startsWith('shared/')) {
          violations.push({
            file: relFile,
            line,
            importPath,
            boundary: 'api-imports-shared',
            suggestion: `api/ must not depend on shared/. The API layer should be independent.`,
          })
        }
      }
    }
  }

  return violations
}

// ---------------------------------------------------------------------------
// Ratchet allowlist
// ---------------------------------------------------------------------------

/**
 * Known violations that exist in the codebase today. These are tracked here so
 * the test passes while preventing NEW violations from being introduced.
 *
 * Format: "file:line:importPath"
 * To reduce a violation, fix the import and remove the entry from this list.
 */
const RATCHET_ALLOWLIST = new Set([
  // cross-feature: reconciliation -> sagas
  'features/reconciliation/pages/detail.tsx:9:@/features/sagas/components/cel-editor',

  // cross-feature: economy -> manifests
  'features/economy/components/editor-graph-panel.tsx:2:@/features/manifests/components/manifest-graph',
  'features/economy/components/apply-resource-modal.tsx:17:@/features/manifests/lib/manifest-graph-model',
  'features/economy/components/conflict-resolution-modal.tsx:12:@/features/manifests/components/manifest-diff-graph',
  'features/economy/components/conflict-resolution-modal.tsx:13:@/features/manifests/lib/manifest-graph-model',
  'features/economy/components/manifest-diff.tsx:3:@/features/manifests/lib/manifest-diff',
  'features/economy/components/manifest-diff.tsx:4:@/features/manifests/lib/manifest-graph-model',
  'features/economy/lib/resource-schema-registry.ts:9:@/features/manifests/lib/manifest-graph-model',
  'features/economy/pages/economy-overview-page.tsx:7:@/features/manifests/components/manifest-graph',
  'features/economy/pages/economy-overview-page.tsx:8:@/features/manifests/pages/manifest-history-table',

  // cross-feature: payments -> sagas, accounts
  'features/payments/pages/payment-detail-query.ts:1:@/features/sagas/components/saga-timeline',
  'features/payments/pages/payment-detail.tsx:11:@/features/sagas/components/saga-timeline',
  'features/payments/pages/dialogs/payment-form-utils.ts:6:@/features/accounts/pages/account-form-utils',

  // cross-feature: cookbook -> sagas
  'features/cookbook/pages/detail.tsx:9:@/features/sagas/components/starlark-editor',

  // cross-feature: accounts -> reference-data
  'features/accounts/pages/[accountId].tsx:16:@/features/reference-data/components/create-valuation-feature-dialog',

  // cross-feature: manifests -> economy
  'features/manifests/components/manifest-graph/index.tsx:10:@/features/economy/components/apply-resource-modal',
  'features/manifests/components/manifest-graph/index.tsx:11:@/features/economy/lib/resource-schema-registry',

  // cross-feature: reference-data -> manifests, sagas
  'features/reference-data/components/execution-context-tab.tsx:3:@/features/manifests/hooks/use-manifest-graph',
  'features/reference-data/components/execution-context-tab.tsx:4:@/features/manifests/hooks/use-event-chain',
  'features/reference-data/components/execution-context-tab.tsx:5:@/features/manifests/components/event-chain-panel',
  'features/reference-data/components/execution-context-tab.tsx:6:@/features/manifests/lib/manifest-graph-model',
  'features/reference-data/components/execution-subgraph.tsx:31:@/features/manifests/lib/manifest-graph-model',
  'features/reference-data/lib/filter-subgraph.ts:5:@/features/manifests/lib/manifest-graph-model',
  'features/reference-data/pages/instruments/index.tsx:7:@/features/sagas/components/cel-editor',
  'features/reference-data/pages/account-types/index.tsx:7:@/features/sagas/components/cel-editor',
  'features/reference-data/pages/account-types/create-account-type-dialog.tsx:13:@/features/sagas/components/cel-editor',
])

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('Frontend import boundaries', () => {
  const violations = findViolations()

  it('should have no cross-feature imports (features/X must not import from features/Y)', () => {
    const crossFeature = violations.filter((v) => v.boundary === 'cross-feature')
    const newViolations = crossFeature.filter(
      (v) => !RATCHET_ALLOWLIST.has(`${v.file}:${v.line}:${v.importPath}`)
    )

    if (newViolations.length > 0) {
      const report = newViolations
        .map(
          (v) =>
            `  ${v.file}:${v.line}\n    import: ${v.importPath}\n    fix: ${v.suggestion}`
        )
        .join('\n\n')
      expect.fail(
        `Found ${newViolations.length} NEW cross-feature import violation(s):\n\n${report}\n\n` +
          `If these are intentional, add them to the RATCHET_ALLOWLIST in this test file.`
      )
    }
  })

  it('should have no imports from features/ in shared/', () => {
    const sharedViolations = violations.filter(
      (v) => v.boundary === 'shared-imports-feature'
    )
    const newViolations = sharedViolations.filter(
      (v) => !RATCHET_ALLOWLIST.has(`${v.file}:${v.line}:${v.importPath}`)
    )

    if (newViolations.length > 0) {
      const report = newViolations
        .map(
          (v) =>
            `  ${v.file}:${v.line}\n    import: ${v.importPath}\n    fix: ${v.suggestion}`
        )
        .join('\n\n')
      expect.fail(
        `Found ${newViolations.length} violation(s) where shared/ imports from features/:\n\n${report}`
      )
    }
  })

  it('should have no imports from features/ or shared/ in api/', () => {
    const apiViolations = violations.filter(
      (v) => v.boundary === 'api-imports-feature' || v.boundary === 'api-imports-shared'
    )
    const newViolations = apiViolations.filter(
      (v) => !RATCHET_ALLOWLIST.has(`${v.file}:${v.line}:${v.importPath}`)
    )

    if (newViolations.length > 0) {
      const report = newViolations
        .map(
          (v) =>
            `  ${v.file}:${v.line}\n    import: ${v.importPath}\n    fix: ${v.suggestion}`
        )
        .join('\n\n')
      expect.fail(
        `Found ${newViolations.length} violation(s) where api/ imports from features/ or shared/:\n\n${report}`
      )
    }
  })

  it('should not have stale entries in the ratchet allowlist', () => {
    const violationKeys = new Set(
      violations.map((v) => `${v.file}:${v.line}:${v.importPath}`)
    )
    const staleEntries = [...RATCHET_ALLOWLIST].filter(
      (entry) => !violationKeys.has(entry)
    )

    if (staleEntries.length > 0) {
      expect.fail(
        `Found ${staleEntries.length} stale ratchet allowlist entry/entries (violations have been fixed - remove them):\n\n` +
          staleEntries.map((e) => `  - ${e}`).join('\n')
      )
    }
  })

  it('should report total violation count for tracking', () => {
    const allowedCount = violations.filter((v) =>
      RATCHET_ALLOWLIST.has(`${v.file}:${v.line}:${v.importPath}`)
    ).length
    const newCount = violations.length - allowedCount

    // Reports the current state and fails if there are any new violations
    console.log(`\nImport boundary violations:`)
    console.log(`  Total:     ${violations.length}`)
    console.log(`  Allowed:   ${allowedCount} (in ratchet allowlist)`)
    console.log(`  New:       ${newCount}`)
    console.log(`  Allowlist: ${RATCHET_ALLOWLIST.size} entries`)

    expect(newCount).toBe(0)
  })
})
