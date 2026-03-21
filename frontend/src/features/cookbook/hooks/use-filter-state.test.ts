import { describe, it, expect } from 'vitest'
import type { CookbookItem, PatternMeta } from './use-cookbook'
import { derivePatternKind, applyFilters } from './use-filter-state'
import type { FilterState } from './use-filter-state'

function makeItem(overrides: Partial<CookbookItem> = {}): CookbookItem {
  return {
    name: 'test-item',
    type: 'registry:pattern',
    title: 'Test Item',
    ...overrides,
  }
}

const emptyFilters: FilterState = { search: '', type: '', category: '', industry: '', kind: '' }

describe('derivePatternKind', () => {
  it('returns null for non-pattern items', () => {
    const item = makeItem({ type: 'registry:ui' })
    expect(derivePatternKind(item)).toBeNull()
  })

  it('returns "foundation" when design_pattern starts with "foundation"', () => {
    const item = makeItem({
      meta: { design_pattern: 'foundation-core' } as PatternMeta,
    })
    expect(derivePatternKind(item)).toBe('foundation')
  })

  it('returns "integration" for gateway category', () => {
    const item = makeItem({ categories: ['gateway'] })
    expect(derivePatternKind(item)).toBe('integration')
  })

  it('returns "integration" for integration category', () => {
    const item = makeItem({ categories: ['integration'] })
    expect(derivePatternKind(item)).toBe('integration')
  })

  it('returns "integration" for payments category', () => {
    const item = makeItem({ categories: ['payments'] })
    expect(derivePatternKind(item)).toBe('integration')
  })

  it('returns "integration" for compliance category', () => {
    const item = makeItem({ categories: ['compliance'] })
    expect(derivePatternKind(item)).toBe('integration')
  })

  it('returns "economy" when item has sagas', () => {
    const item = makeItem({
      meta: { provides: { sagas: ['saga1'] } } as PatternMeta,
    })
    expect(derivePatternKind(item)).toBe('economy')
  })

  it('returns "definition" for pattern without special markers', () => {
    const item = makeItem({ categories: ['general'] })
    expect(derivePatternKind(item)).toBe('definition')
  })

  it('prioritizes foundation over integration', () => {
    const item = makeItem({
      categories: ['gateway'],
      meta: { design_pattern: 'foundation-base' } as PatternMeta,
    })
    expect(derivePatternKind(item)).toBe('foundation')
  })
})

describe('applyFilters', () => {
  const items: CookbookItem[] = [
    makeItem({ name: 'banking', title: 'Banking Pattern', type: 'registry:pattern', categories: ['finance'], meta: { industries: ['banking'] } as PatternMeta }),
    makeItem({ name: 'energy-ui', title: 'Energy Dashboard', type: 'registry:ui', categories: ['energy'] }),
    makeItem({ name: 'carbon', title: 'Carbon Credits', type: 'registry:pattern', categories: ['compliance'], meta: { industries: ['energy'], provides: { sagas: ['saga1'] } } as PatternMeta }),
  ]

  it('returns all items with empty filters', () => {
    expect(applyFilters(items, emptyFilters)).toHaveLength(3)
  })

  it('filters by type: pattern', () => {
    const result = applyFilters(items, { ...emptyFilters, type: 'pattern' })
    expect(result).toHaveLength(2)
    expect(result.every((i) => i.type === 'registry:pattern')).toBe(true)
  })

  it('filters by type: ui', () => {
    const result = applyFilters(items, { ...emptyFilters, type: 'ui' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('energy-ui')
  })

  it('filters by category', () => {
    const result = applyFilters(items, { ...emptyFilters, category: 'finance' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('banking')
  })

  it('filters by industry', () => {
    const result = applyFilters(items, { ...emptyFilters, industry: 'energy' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('carbon')
  })

  it('filters by search term (case insensitive)', () => {
    const result = applyFilters(items, { ...emptyFilters, search: 'carbon' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('carbon')
  })

  it('search matches against name, title, and description', () => {
    const result = applyFilters(items, { ...emptyFilters, search: 'Dashboard' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('energy-ui')
  })

  it('filters by kind (integration)', () => {
    const result = applyFilters(items, { ...emptyFilters, kind: 'integration' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('carbon')
  })

  it('combines multiple filters', () => {
    const result = applyFilters(items, {
      ...emptyFilters,
      type: 'pattern',
      industry: 'banking',
    })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('banking')
  })

  it('returns empty when no items match', () => {
    const result = applyFilters(items, { ...emptyFilters, search: 'nonexistent' })
    expect(result).toHaveLength(0)
  })

  it('filters by unknown type returns empty', () => {
    const result = applyFilters(items, { ...emptyFilters, type: 'unknown' })
    expect(result).toHaveLength(0)
  })
})
