import type { ManifestNodeType } from './manifest-graph-model'

export interface NodeTypeConfig {
  /** CSS variable or color value for theming this node type */
  color: string
  /** Human-readable plural label (e.g. "Instruments") */
  label: string
  /** ELK layer priority: higher values are placed earlier (top in DOWN layout) */
  layerPriority: string
}

/**
 * Centralized registry for all manifest node type configuration.
 * Single source of truth for colors, labels, and layer priorities.
 */
export const NODE_TYPE_REGISTRY: Record<ManifestNodeType, NodeTypeConfig> = {
  instrument: {
    color: 'var(--graph-instrument)',
    label: 'Instruments',
    layerPriority: '40',
  },
  account_type: {
    color: 'var(--graph-account-type)',
    label: 'Account Types',
    layerPriority: '30',
  },
  valuation_rule: {
    color: 'var(--graph-valuation-rule)',
    label: 'Valuation Rules',
    layerPriority: '20',
  },
  saga: {
    color: 'var(--graph-saga)',
    label: 'Sagas',
    layerPriority: '10',
  },
  payment_rail: {
    color: 'var(--graph-payment-rail)',
    label: 'Payment Rails',
    layerPriority: '9',
  },
  party_type: {
    color: 'var(--graph-party-type)',
    label: 'Party Types',
    layerPriority: '8',
  },
  mapping: {
    color: 'var(--graph-mapping)',
    label: 'Mappings',
    layerPriority: '7',
  },
  operational_gateway: {
    color: 'var(--graph-operational-gateway)',
    label: 'Operational Gateway',
    layerPriority: '6',
  },
  provider_connection: {
    color: 'var(--graph-provider-connection)',
    label: 'Provider Connections',
    layerPriority: '5',
  },
  instruction_route: {
    color: 'var(--graph-instruction-route)',
    label: 'Instruction Routes',
    layerPriority: '4',
  },
  event_channel: {
    color: 'var(--graph-event-channel)',
    label: 'Event Channels',
    layerPriority: '15',
  },
  market_data: {
    color: 'var(--graph-market-data)',
    label: 'Market Data',
    layerPriority: '3',
  },
  organization: {
    color: 'var(--graph-organization)',
    label: 'Organizations',
    layerPriority: '2',
  },
  internal_account: {
    color: 'var(--graph-internal-account)',
    label: 'Internal Accounts',
    layerPriority: '1',
  },
}

/** Derived view: { color, label } per node type, compatible with existing NODE_THEMES usage. */
let _nodeThemes: Record<ManifestNodeType, { color: string; label: string }> | null = null
export function getNodeThemes(): Record<ManifestNodeType, { color: string; label: string }> {
  if (_nodeThemes) return _nodeThemes
  _nodeThemes = {} as Record<ManifestNodeType, { color: string; label: string }>
  for (const [type, config] of Object.entries(NODE_TYPE_REGISTRY)) {
    _nodeThemes[type as ManifestNodeType] = { color: config.color, label: config.label }
  }
  return _nodeThemes
}

/** Derived view: layer priority per node type, compatible with existing LAYER_PRIORITY usage. */
let _layerPriority: Record<ManifestNodeType, string> | null = null
export function getLayerPriority(): Record<ManifestNodeType, string> {
  if (_layerPriority) return _layerPriority
  _layerPriority = {} as Record<ManifestNodeType, string>
  for (const [type, config] of Object.entries(NODE_TYPE_REGISTRY)) {
    _layerPriority[type as ManifestNodeType] = config.layerPriority
  }
  return _layerPriority
}
