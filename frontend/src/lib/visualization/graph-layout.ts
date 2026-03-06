import type { Node, Edge } from '@xyflow/react'
import ELK from 'elkjs/lib/elk.bundled.js'

const elk = new ELK()

export const NODE_WIDTH = 200
export const NODE_BASE_HEIGHT = 40
export const NODE_PADDING = 20

export const EDGE_STYLES = {
  registryDependencies: { stroke: '#3b82f6', strokeWidth: 2, strokeDasharray: undefined },
  composes_with: { stroke: '#22c55e', strokeWidth: 1.5, strokeDasharray: '6 3' },
  extends: { stroke: '#8b5cf6', strokeWidth: 3, strokeDasharray: undefined },
  conflicts_with: { stroke: '#ef4444', strokeWidth: 1.5, strokeDasharray: '4 4' },
} as const

export type RelationshipType = keyof typeof EDGE_STYLES

export interface ELKLayoutOptions {
  algorithm?: string
  direction?: string
  nodeNodeSpacing?: string
  layerSpacing?: string
}

export interface LayoutNode {
  id: string
  width: number
  height: number
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
  }))

  const elkEdges = edges.map((e) => ({
    id: e.id,
    sources: [e.source],
    targets: [e.target],
  }))

  const layout = await elk.layout({
    id: 'root',
    layoutOptions: {
      'elk.algorithm': options?.algorithm ?? 'layered',
      'elk.direction': options?.direction ?? 'DOWN',
      'elk.spacing.nodeNode': options?.nodeNodeSpacing ?? '60',
      'elk.layered.spacing.nodeNodeBetweenLayers': options?.layerSpacing ?? '100',
    },
    children: elkNodes,
    edges: elkEdges,
  })

  return nodes.map((n) => {
    const elkNode = layout.children?.find((c) => c.id === n.id)
    return nodeDataFactory(n.id, { x: elkNode?.x ?? 0, y: elkNode?.y ?? 0 })
  })
}
