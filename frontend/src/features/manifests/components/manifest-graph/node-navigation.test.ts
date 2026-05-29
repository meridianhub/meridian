import { describe, it, expect } from 'vitest'
import type { ManifestNode, ManifestNodeType } from '../../lib/manifest-graph-model'
import { getNodeNavigationPath } from './node-navigation'

function node(type: ManifestNodeType, overrides: Partial<ManifestNode> = {}): ManifestNode {
  return { id: `${type}:x`, type, label: 'Node', data: {}, ...overrides }
}

describe('getNodeNavigationPath', () => {
  it.each<[ManifestNodeType, string]>([
    ['instrument', '/reference-data/instruments'],
    ['account_type', '/reference-data/account-types'],
    ['valuation_rule', '/reference-data/valuation-rules'],
    ['organization', '/reference-data/nodes'],
    ['internal_account', '/internal-accounts'],
    ['mapping', '/gateway-mappings'],
    ['party_type', '/parties'],
  ])('routes %s to its static destination %s', (type, expected) => {
    expect(getNodeNavigationPath(node(type))).toBe(expected)
  })

  it.each<ManifestNodeType>([
    'payment_rail',
    'operational_gateway',
    'provider_connection',
    'instruction_route',
  ])('routes %s to the reference-data hub', (type) => {
    expect(getNodeNavigationPath(node(type))).toBe('/reference-data')
  })

  it('routes a saga to its starlark config using the node label', () => {
    expect(getNodeNavigationPath(node('saga', { label: 'usage_to_value' }))).toBe(
      '/starlark-config/usage_to_value',
    )
  })

  it('routes market_data with a code to that code page', () => {
    expect(getNodeNavigationPath(node('market_data', { data: { code: 'SPOT' } }))).toBe('/market-data/SPOT')
  })

  it('routes market_data without a code to the market-data index', () => {
    expect(getNodeNavigationPath(node('market_data'))).toBe('/market-data')
  })

  it('returns null for node types with no destination', () => {
    expect(getNodeNavigationPath(node('event_channel'))).toBeNull()
  })
})
