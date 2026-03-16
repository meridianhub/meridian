import { describe, it, expect } from 'vitest'
import {
  NODE_TYPE_REGISTRY,
  getNodeThemes,
  getLayerPriority,
} from './node-type-registry'
import type { ManifestNodeType } from './manifest-graph-model'

describe('NODE_TYPE_REGISTRY', () => {
  const ALL_TYPES: ManifestNodeType[] = [
    'instrument',
    'account_type',
    'valuation_rule',
    'saga',
    'payment_rail',
    'party_type',
    'mapping',
    'operational_gateway',
    'provider_connection',
    'instruction_route',
    'market_data',
    'organization',
    'internal_account',
  ]

  it('has entries for all 13 ManifestNodeType values', () => {
    expect(Object.keys(NODE_TYPE_REGISTRY)).toHaveLength(13)
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

  it('assigns CSS variables for new node types', () => {
    expect(NODE_TYPE_REGISTRY.payment_rail.color).toBe('var(--graph-payment-rail)')
    expect(NODE_TYPE_REGISTRY.party_type.color).toBe('var(--graph-party-type)')
    expect(NODE_TYPE_REGISTRY.mapping.color).toBe('var(--graph-mapping)')
    expect(NODE_TYPE_REGISTRY.operational_gateway.color).toBe('var(--graph-operational-gateway)')
    expect(NODE_TYPE_REGISTRY.provider_connection.color).toBe('var(--graph-provider-connection)')
    expect(NODE_TYPE_REGISTRY.instruction_route.color).toBe('var(--graph-instruction-route)')
    expect(NODE_TYPE_REGISTRY.market_data.color).toBe('var(--graph-market-data)')
    expect(NODE_TYPE_REGISTRY.organization.color).toBe('var(--graph-organization)')
    expect(NODE_TYPE_REGISTRY.internal_account.color).toBe('var(--graph-internal-account)')
  })

  it('preserves original labels', () => {
    expect(NODE_TYPE_REGISTRY.instrument.label).toBe('Instruments')
    expect(NODE_TYPE_REGISTRY.account_type.label).toBe('Account Types')
    expect(NODE_TYPE_REGISTRY.valuation_rule.label).toBe('Valuation Rules')
    expect(NODE_TYPE_REGISTRY.saga.label).toBe('Sagas')
  })

  it('assigns labels for new node types', () => {
    expect(NODE_TYPE_REGISTRY.payment_rail.label).toBe('Payment Rails')
    expect(NODE_TYPE_REGISTRY.party_type.label).toBe('Party Types')
    expect(NODE_TYPE_REGISTRY.mapping.label).toBe('Mappings')
    expect(NODE_TYPE_REGISTRY.operational_gateway.label).toBe('Operational Gateway')
    expect(NODE_TYPE_REGISTRY.provider_connection.label).toBe('Provider Connections')
    expect(NODE_TYPE_REGISTRY.instruction_route.label).toBe('Instruction Routes')
    expect(NODE_TYPE_REGISTRY.market_data.label).toBe('Market Data')
    expect(NODE_TYPE_REGISTRY.organization.label).toBe('Organizations')
    expect(NODE_TYPE_REGISTRY.internal_account.label).toBe('Internal Accounts')
  })

  it('preserves original layer priorities', () => {
    expect(NODE_TYPE_REGISTRY.instrument.layerPriority).toBe('40')
    expect(NODE_TYPE_REGISTRY.account_type.layerPriority).toBe('30')
    expect(NODE_TYPE_REGISTRY.valuation_rule.layerPriority).toBe('20')
    expect(NODE_TYPE_REGISTRY.saga.layerPriority).toBe('10')
  })

  it('assigns layer priorities for new node types', () => {
    expect(NODE_TYPE_REGISTRY.payment_rail.layerPriority).toBe('9')
    expect(NODE_TYPE_REGISTRY.party_type.layerPriority).toBe('8')
    expect(NODE_TYPE_REGISTRY.mapping.layerPriority).toBe('7')
    expect(NODE_TYPE_REGISTRY.operational_gateway.layerPriority).toBe('6')
    expect(NODE_TYPE_REGISTRY.provider_connection.layerPriority).toBe('5')
    expect(NODE_TYPE_REGISTRY.instruction_route.layerPriority).toBe('4')
    expect(NODE_TYPE_REGISTRY.market_data.layerPriority).toBe('3')
    expect(NODE_TYPE_REGISTRY.organization.layerPriority).toBe('2')
    expect(NODE_TYPE_REGISTRY.internal_account.layerPriority).toBe('1')
  })

  it('layer priorities are in descending order from instrument to instruction_route', () => {
    const priorities = ALL_TYPES.map((t) => parseInt(NODE_TYPE_REGISTRY[t].layerPriority, 10))
    for (let i = 0; i < priorities.length - 1; i++) {
      expect(priorities[i]).toBeGreaterThan(priorities[i + 1])
    }
  })
})

describe('getNodeThemes', () => {
  it('returns a Record<ManifestNodeType, { color, label }> for all 13 types', () => {
    const themes = getNodeThemes()
    expect(Object.keys(themes)).toHaveLength(13)
    expect(themes.instrument).toEqual({
      color: 'var(--graph-instrument)',
      label: 'Instruments',
    })
    expect(themes.saga).toEqual({
      color: 'var(--graph-saga)',
      label: 'Sagas',
    })
    expect(themes.payment_rail).toEqual({
      color: 'var(--graph-payment-rail)',
      label: 'Payment Rails',
    })
    expect(themes.operational_gateway).toEqual({
      color: 'var(--graph-operational-gateway)',
      label: 'Operational Gateway',
    })
    expect(themes.instruction_route).toEqual({
      color: 'var(--graph-instruction-route)',
      label: 'Instruction Routes',
    })
    expect(themes.market_data).toEqual({
      color: 'var(--graph-market-data)',
      label: 'Market Data',
    })
    expect(themes.organization).toEqual({
      color: 'var(--graph-organization)',
      label: 'Organizations',
    })
    expect(themes.internal_account).toEqual({
      color: 'var(--graph-internal-account)',
      label: 'Internal Accounts',
    })
  })

  it('returns the same reference on repeated calls (memoized)', () => {
    const a = getNodeThemes()
    const b = getNodeThemes()
    expect(a).toBe(b)
  })
})

describe('getLayerPriority', () => {
  it('returns a Record<ManifestNodeType, string> for all 13 types', () => {
    const priority = getLayerPriority()
    expect(Object.keys(priority)).toHaveLength(13)
    expect(priority.instrument).toBe('40')
    expect(priority.account_type).toBe('30')
    expect(priority.valuation_rule).toBe('20')
    expect(priority.saga).toBe('10')
    expect(priority.payment_rail).toBe('9')
    expect(priority.party_type).toBe('8')
    expect(priority.mapping).toBe('7')
    expect(priority.operational_gateway).toBe('6')
    expect(priority.provider_connection).toBe('5')
    expect(priority.instruction_route).toBe('4')
    expect(priority.market_data).toBe('3')
    expect(priority.organization).toBe('2')
    expect(priority.internal_account).toBe('1')
  })

  it('returns the same reference on repeated calls (memoized)', () => {
    const a = getLayerPriority()
    const b = getLayerPriority()
    expect(a).toBe(b)
  })
})
