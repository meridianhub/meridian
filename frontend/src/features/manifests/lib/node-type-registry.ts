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
