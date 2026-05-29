import type { ManifestNode } from '../../lib/manifest-graph-model'

/** Node types that all route to the generic reference-data hub on double-click. */
const REFERENCE_DATA_TYPES = new Set<ManifestNode['type']>([
  'payment_rail',
  'operational_gateway',
  'provider_connection',
  'instruction_route',
])

/** Static double-click destinations keyed by node type. */
const STATIC_ROUTES: Partial<Record<ManifestNode['type'], string>> = {
  instrument: '/reference-data/instruments',
  account_type: '/reference-data/account-types',
  valuation_rule: '/reference-data/valuation-rules',
  organization: '/reference-data/nodes',
  internal_account: '/internal-accounts',
  mapping: '/gateway-mappings',
  party_type: '/parties',
}

/**
 * Resolve the route a node navigates to on double-click, or null if the node
 * type has no destination. `saga` and `market_data` are dynamic (derived from
 * node data); everything else is a static or reference-data route.
 */
export function getNodeNavigationPath(node: ManifestNode): string | null {
  if (node.type === 'saga') return `/starlark-config/${encodeURIComponent(node.label)}`
  if (node.type === 'market_data') {
    const code = node.data.code as string | undefined
    return code ? `/market-data/${encodeURIComponent(code)}` : '/market-data'
  }
  if (REFERENCE_DATA_TYPES.has(node.type)) return '/reference-data'
  return STATIC_ROUTES[node.type] ?? null
}
