import type { Node, Edge } from '@xyflow/react'
import ELK from 'elkjs/lib/elk.bundled.js'

const elk = new ELK()

export const NODE_WIDTH = 200
export const NODE_BASE_HEIGHT = 40
export const NODE_PADDING = 20

// Edge colours follow the marketing cookbook viewer's encoding: ink for
// dependencies, credit green for composition, dark saga tone for extends,
// destructive only for genuine conflicts. Matches the on-page legend.
export const EDGE_STYLES = {
  registryDependencies: { stroke: 'var(--graph-instrument)', strokeWidth: 2, strokeDasharray: undefined },
  composes_with: { stroke: 'var(--graph-account-type)', strokeWidth: 1.5, strokeDasharray: '6 3' },
  extends: { stroke: 'var(--graph-saga)', strokeWidth: 3, strokeDasharray: undefined },
  conflicts_with: { stroke: 'var(--destructive)', strokeWidth: 1.5, strokeDasharray: '4 4' },
} as const

export type RelationshipType = keyof typeof EDGE_STYLES

export interface ELKLayoutOptions {
  algorithm?: string
  direction?: string
  nodeNodeSpacing?: string
  layerSpacing?: string
  /** Additional ELK layout options passed through verbatim. */
  extra?: Record<string, string>
}

export interface LayoutNode {
  id: string
  width: number
  height: number
  layoutOptions?: Record<string, string>
}

/**
 * Layout nodes and edges using the ELK (Eclipse Layout Kernel) algorithm.
 * Returns positioned nodes ready for ReactFlow.
 */
export async function layoutWithELK<T extends Record<string, unknown>>(
  nodes: LayoutNode[],
  edges: Edge[],
  nodeDataFactory: (id: string, position: { x: number; y: number }) => Node<T>,
  options?: ELKLayoutOptions,
): Promise<Node<T>[]> {
  const elkNodes = nodes.map((n) => ({
    id: n.id,
    width: n.width,
    height: n.height,
    ...(n.layoutOptions ? { layoutOptions: n.layoutOptions } : {}),
  }))

  const elkEdges = edges.map((e) => ({
    id: e.id,
    sources: [e.source],
    targets: [e.target],
  }))

  const algorithm = options?.algorithm ?? 'layered'
  const layoutOptions: Record<string, string> = {
    'elk.algorithm': algorithm,
    'elk.spacing.nodeNode': options?.nodeNodeSpacing ?? '60',
  }

  if (algorithm === 'layered') {
    layoutOptions['elk.direction'] = options?.direction ?? 'DOWN'
    layoutOptions['elk.layered.spacing.nodeNodeBetweenLayers'] = options?.layerSpacing ?? '100'
  }

  if (options?.extra) {
    Object.assign(layoutOptions, options.extra)
  }

  const layout = await elk.layout({
    id: 'root',
    layoutOptions,
    children: elkNodes,
    edges: elkEdges,
  })

  const childrenById = new Map((layout.children ?? []).map((child) => [child.id, child]))

  return nodes.map((n) => {
    const elkNode = childrenById.get(n.id)
    if (!elkNode || elkNode.x == null || elkNode.y == null) {
      throw new Error(`ELK did not return coordinates for node "${n.id}"`)
    }
    return nodeDataFactory(n.id, { x: elkNode.x, y: elkNode.y })
  })
}
