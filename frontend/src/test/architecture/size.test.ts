/**
 * Architecture test: file size and component complexity.
 *
 * Enforces two limits:
 *   1. Component files (.tsx) must not exceed 300 lines.
 *   2. Any source file (.ts/.tsx) must not exceed 500 lines.
 *
 * A ratchet allowlist (knownOversizedFiles) captures current violations so the
 * test passes today while preventing new violations. The allowlist must only
 * shrink over time - when a file is refactored below the limit, remove it.
 */
import { describe, it, expect } from 'vitest'
import * as fs from 'node:fs'
import * as path from 'node:path'

// ---------------------------------------------------------------------------
// Limits
// ---------------------------------------------------------------------------

const COMPONENT_LINE_LIMIT = 300 // .tsx files
const FILE_LINE_LIMIT = 500 // any .ts / .tsx file

// ---------------------------------------------------------------------------
// Ratchet allowlist - known oversized files with their current line counts.
// When you refactor a file below the limit, REMOVE it from here.
// NEVER increase a count - only decrease or remove entries.
// ---------------------------------------------------------------------------

const knownOversizedFiles: Record<string, number> = {
  // .tsx files exceeding 300-line component limit (and some also >500 overall)
  'src/features/manifests/components/manifest-graph.tsx': 1124,
  'src/features/manifests/components/manifest-diff-graph.tsx': 657,
  'src/features/reconciliation/pages/detail.tsx': 638,
  'src/features/cookbook/components/saga-flow.tsx': 611,
  'src/App.tsx': 390,
  'src/features/reference-data/pages/account-types/create-account-type-dialog.tsx': 516,
  'src/features/accounts/pages/[accountId].tsx': 497,
  'src/features/economy/components/deploy-wizard.tsx': 490,
  'src/features/mappings/pages/[mappingId].tsx': 488,
  'src/features/cookbook/components/composition-graph.tsx': 450,
  'src/features/reference-data/components/create-valuation-feature-dialog.tsx': 401,
  'src/contexts/auth-context.tsx': 376,
  'src/features/internal-accounts/pages/[accountId].tsx': 369,
  'src/features/parties/pages/dialogs/register-associations-dialog.tsx': 368,
  'src/features/internal-accounts/pages/create-internal-account-dialog.tsx': 367,
  'src/features/cookbook/pages/detail.tsx': 367,
  'src/shared/data-table.tsx': 355,
  'src/components/layout/sidebar.tsx': 356,
  'src/features/reference-data/pages/instruments/register-instrument-dialog.tsx': 329,
  'src/features/market-data/pages/[datasetCode].tsx': 322,
  'src/features/tenants/pages/[tenantId].tsx': 316,
  'src/features/sagas/pages/create-saga-draft-dialog.tsx': 312,
  'src/features/reference-data/pages/instruments/index.tsx': 317,
  'src/features/mappings/pages/dialogs/create-mapping-dialog.tsx': 308,
  // .ts files exceeding 500-line limit
  'src/features/manifests/lib/manifest-graph-model.ts': 540,
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const FRONTEND_ROOT = path.resolve(__dirname, '../../..')

function collectSourceFiles(dir: string): string[] {
  const results: string[] = []
  const entries = fs.readdirSync(dir, { withFileTypes: true })
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      // Skip excluded directories
      if (entry.name === 'node_modules' || entry.name === 'gen') continue
      results.push(...collectSourceFiles(fullPath))
    } else if (entry.isFile()) {
      const name = entry.name
      // Include .ts and .tsx, exclude tests
      if (
        (name.endsWith('.ts') || name.endsWith('.tsx')) &&
        !name.includes('.test.') &&
        !name.includes('.spec.')
      ) {
        results.push(fullPath)
      }
    }
  }
  return results
}

function lineCount(filePath: string): number {
  const content = fs.readFileSync(filePath, 'utf-8')
  if (content.length === 0) return 0
  // Count newline characters, plus one if the file lacks a trailing newline
  let count = 0
  for (let i = 0; i < content.length; i++) {
    if (content[i] === '\n') count++
  }
  if (!content.endsWith('\n')) count++
  return count
}

function relativePath(filePath: string): string {
  return path.relative(FRONTEND_ROOT, filePath).split(path.sep).join('/')
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('frontend file size limits', () => {
  const srcDir = path.join(FRONTEND_ROOT, 'src')
  const allFiles = collectSourceFiles(srcDir)

  it('no component file (.tsx) exceeds the line limit', () => {
    const violations: string[] = []

    for (const file of allFiles) {
      if (!file.endsWith('.tsx')) continue
      const rel = relativePath(file)
      const lines = lineCount(file)

      if (lines <= COMPONENT_LINE_LIMIT) {
        // If the file was in the allowlist but is now under the limit, that's
        // good - the test still passes. The allowlist entry is stale and
        // should be removed, but we flag it separately below.
        continue
      }

      const allowed = knownOversizedFiles[rel]
      if (allowed === undefined) {
        violations.push(
          `NEW VIOLATION: ${rel} has ${lines} lines (limit: ${COMPONENT_LINE_LIMIT}). ` +
            `Consider splitting into smaller components.`,
        )
      } else if (lines > allowed) {
        violations.push(
          `REGRESSION: ${rel} grew from ${allowed} to ${lines} lines. ` +
            `Reduce it or update the allowlist downward only.`,
        )
      }
    }

    expect(violations, violations.join('\n')).toEqual([])
  })

  it('no source file exceeds the absolute line limit', () => {
    const violations: string[] = []

    for (const file of allFiles) {
      const rel = relativePath(file)
      const lines = lineCount(file)

      if (lines <= FILE_LINE_LIMIT) continue

      const allowed = knownOversizedFiles[rel]
      if (allowed === undefined) {
        violations.push(
          `NEW VIOLATION: ${rel} has ${lines} lines (limit: ${FILE_LINE_LIMIT}). ` +
            `Consider splitting into smaller modules.`,
        )
      } else if (lines > allowed) {
        violations.push(
          `REGRESSION: ${rel} grew from ${allowed} to ${lines} lines. ` +
            `Reduce it or update the allowlist downward only.`,
        )
      }
    }

    expect(violations, violations.join('\n')).toEqual([])
  })

  it('allowlist entries are still necessary (ratchet can only shrink)', () => {
    const staleEntries: string[] = []

    for (const [rel, allowedLines] of Object.entries(knownOversizedFiles)) {
      const fullPath = path.join(FRONTEND_ROOT, rel)

      if (!fs.existsSync(fullPath)) {
        staleEntries.push(`REMOVED: ${rel} no longer exists - remove from allowlist`)
        continue
      }

      const lines = lineCount(fullPath)
      const isComponent = rel.endsWith('.tsx')
      const limit = isComponent ? COMPONENT_LINE_LIMIT : FILE_LINE_LIMIT

      if (lines <= limit) {
        staleEntries.push(
          `RESOLVED: ${rel} is now ${lines} lines (limit: ${limit}) - remove from allowlist`,
        )
      } else if (lines < allowedLines) {
        staleEntries.push(
          `SHRUNK: ${rel} is now ${lines} lines (was ${allowedLines}) - ` +
            `update allowlist from ${allowedLines} to ${lines}`,
        )
      }
    }

    expect(staleEntries, staleEntries.join('\n')).toEqual([])
  })
})
