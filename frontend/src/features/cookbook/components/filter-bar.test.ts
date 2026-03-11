import { describe, it, expect } from 'vitest'
import { applyFilters, type FilterState } from '../hooks/use-filter-state'
import type { CookbookItem } from '../hooks/use-cookbook'

const mockItems: CookbookItem[] = [
  {
    name: 'energy-trading',
    type: 'registry:pattern',
    title: 'Energy Trading',
    description: 'Buy and sell electricity on wholesale markets',
    categories: ['energy', 'trading'],
    meta: {
      complexity: 7,
      design_pattern: 'saga',
      industries: ['energy', 'utilities'],
    },
  },
  {
    name: 'carbon-offset',
    type: 'registry:pattern',
    title: 'Carbon Offset',
    description: 'Track carbon credits and offsets',
    categories: ['carbon', 'compliance'],
    meta: {
      complexity: 5,
      industries: ['energy', 'finance'],
    },
  },
  {
    name: 'balance-card',
    type: 'registry:ui',
    title: 'Balance Card',
    description: 'Displays account balance with currency formatting',
    categories: ['accounts'],
    meta: {
      feature_module: 'accounts',
      configurable: true,
    },
  },
]

const emptyFilters: FilterState = { search: '', type: '', category: '', industry: '', kind: '' }

describe('applyFilters', () => {
  it('returns all items when no filters active', () => {
    const result = applyFilters(mockItems, emptyFilters)
    expect(result).toHaveLength(3)
  })

  it('filters by type pattern', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, type: 'pattern' })
    expect(result).toHaveLength(2)
    expect(result.every((i) => i.type === 'registry:pattern')).toBe(true)
  })

  it('filters by type ui', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, type: 'ui' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('balance-card')
  })

  it('filters by category', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, category: 'energy' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('energy-trading')
  })

  it('filters by industry', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, industry: 'finance' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('carbon-offset')
  })

  it('filters by search term in title', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, search: 'balance' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('balance-card')
  })

  it('filters by search term in description', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, search: 'electricity' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('energy-trading')
  })

  it('search is case-insensitive', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, search: 'CARBON' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('carbon-offset')
  })

  it('combines multiple filters with AND logic', () => {
    const result = applyFilters(mockItems, {
      type: 'pattern',
      industry: 'energy',
      category: '',
      search: '',
    })
    expect(result).toHaveLength(2)
  })

  it('returns empty when no items match', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, search: 'nonexistent' })
    expect(result).toHaveLength(0)
  })

  it('handles items without categories or meta gracefully', () => {
    const sparse: CookbookItem[] = [
      { name: 'bare', type: 'registry:pattern', title: 'Bare Item' },
    ]
    const result = applyFilters(sparse, { ...emptyFilters, category: 'anything' })
    expect(result).toHaveLength(0)
  })

  it('handles items without meta.industries for industry filter', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, industry: 'utilities' })
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('energy-trading')
  })

  it('filters out all items for unknown type value', () => {
    const result = applyFilters(mockItems, { ...emptyFilters, type: 'invalid' })
    expect(result).toHaveLength(0)
  })
})
