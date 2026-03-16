import { describe, it, expect } from 'vitest'
import {
  NODE_TYPE_REGISTRY,
  getNodeThemes,
  getLayerPriority,
  type NodeTypeConfig,
} from './node-type-registry'
import type { ManifestNodeType } from './manifest-graph-model'

describe('NODE_TYPE_REGISTRY', () => {
  const ALL_TYPES: ManifestNodeType[] = [
    'instrument',
    'account_type',
    'valuation_rule',
    'saga',
  ]

  it('has entries for all current ManifestNodeType values', () => {
    for (const type of ALL_TYPES) {
      expect(NODE_TYPE_REGISTRY[type]).toBeDefined()
    }
  })

  it('each entry has required fields', () => {
    for (const type of ALL_TYPES) {
      const config = NODE_TYPE_REGISTRY[type]
      expect(config.color).toBeTruthy()
      expect(config.label).toBeTruthy()
      expect(config.layerPriority).toMatch(/^\d+$/)
    }
  })

  it('preserves original theme colors using CSS variables', () => {
    expect(NODE_TYPE_REGISTRY.instrument.color).toBe('var(--graph-instrument)')
    expect(NODE_TYPE_REGISTRY.account_type.color).toBe('var(--graph-account-type)')
    expect(NODE_TYPE_REGISTRY.valuation_rule.color).toBe('var(--graph-valuation-rule)')
    expect(NODE_TYPE_REGISTRY.saga.color).toBe('var(--graph-saga)')
  })

  it('preserves original labels', () => {
    expect(NODE_TYPE_REGISTRY.instrument.label).toBe('Instruments')
    expect(NODE_TYPE_REGISTRY.account_type.label).toBe('Account Types')
    expect(NODE_TYPE_REGISTRY.valuation_rule.label).toBe('Valuation Rules')
    expect(NODE_TYPE_REGISTRY.saga.label).toBe('Sagas')
  })

  it('preserves original layer priorities', () => {
    expect(NODE_TYPE_REGISTRY.instrument.layerPriority).toBe('40')
    expect(NODE_TYPE_REGISTRY.account_type.layerPriority).toBe('30')
    expect(NODE_TYPE_REGISTRY.valuation_rule.layerPriority).toBe('20')
    expect(NODE_TYPE_REGISTRY.saga.layerPriority).toBe('10')
  })
})

describe('getNodeThemes', () => {
  it('returns a Record<ManifestNodeType, { color, label }>', () => {
    const themes = getNodeThemes()
    expect(themes.instrument).toEqual({
      color: 'var(--graph-instrument)',
      label: 'Instruments',
    })
    expect(themes.saga).toEqual({
      color: 'var(--graph-saga)',
      label: 'Sagas',
    })
  })

  it('returns the same reference on repeated calls (memoized)', () => {
    const a = getNodeThemes()
    const b = getNodeThemes()
    expect(a).toBe(b)
  })
})

describe('getLayerPriority', () => {
  it('returns a Record<ManifestNodeType, string>', () => {
    const priority = getLayerPriority()
    expect(priority.instrument).toBe('40')
    expect(priority.account_type).toBe('30')
    expect(priority.valuation_rule).toBe('20')
    expect(priority.saga).toBe('10')
  })

  it('returns the same reference on repeated calls (memoized)', () => {
    const a = getLayerPriority()
    const b = getLayerPriority()
    expect(a).toBe(b)
  })
})
