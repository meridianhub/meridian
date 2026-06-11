import type { Node, Edge } from '@xyflow/react'
import {
  layoutWithELK,
  NODE_WIDTH,
  NODE_BASE_HEIGHT,
  NODE_PADDING,
} from '@/lib/visualization/graph-layout'
import {
  type ManifestEdge,
  type ManifestNodeType,
  type ManifestGraph as ManifestGraphModel,
} from '../../lib/manifest-graph-model'
import { NODE_TYPE_REGISTRY } from '../../lib/node-type-registry'
import type { ManifestNodeData } from './node-renderers'

/** Edge stroke styling keyed by relationship type. */
export const MANIFEST_EDGE_STYLES: Record<string, React.CSSProperties> = {
  allowed_by: { stroke: 'var(--graph-instrument)', strokeWidth: 2 },
  converts_from: { stroke: 'var(--graph-valuation-rule)', strokeWidth: 1.5, strokeDasharray: '6 3' },
  converts_to: { stroke: 'var(--graph-valuation-rule)', strokeWidth: 1.5 },
}

/** Pick the closest side handle pair based on relative node positions. */
export function pickHandles(
  src: { x: number; y: number },
  tgt: { x: number; y: number },
): { sourceHandle: string; targetHandle: string } {
  const dx = tgt.x - src.x
  const dy = tgt.y - src.y
  if (Math.abs(dx) > Math.abs(dy)) {
    return dx > 0
      ? { sourceHandle: 'right', targetHandle: 'left-target' }
      : { sourceHandle: 'left', targetHandle: 'right-target' }
  }
  return dy > 0
    ? { sourceHandle: 'bottom', targetHandle: 'top-target' }
    : { sourceHandle: 'top', targetHandle: 'bottom-target' }
}

/** Convert manifest edges into React Flow edges with styling and arrow markers. */
export function buildReactFlowEdges(manifestEdges: ManifestEdge[]): Edge[] {
  return manifestEdges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    style: MANIFEST_EDGE_STYLES[e.relationship] ?? {},
    markerEnd:
      e.relationship === 'converts_to' || e.relationship === 'allowed_by'
        ? { type: 'arrowclosed' as const, color: (MANIFEST_EDGE_STYLES[e.relationship]?.stroke as string) ?? 'var(--muted-foreground)' }
        : undefined,
    data: { relationship: e.relationship },
  }))
}

/** Count instrument connections per account_type node from allowed_by edges. */
function countConnectedInstruments(edges: ManifestEdge[]): Map<string, number> {
  const connected = new Map<string, number>()
  for (const e of edges) {
    if (e.relationship === 'allowed_by') {
      connected.set(e.source, (connected.get(e.source) ?? 0) + 1)
    }
  }
  return connected
}

/**
 * Run ELK force layout over the visible subgraph, returning positioned React
 * Flow nodes and edges with their nearest-side handles assigned.
 */
export async function layoutManifestGraph(
  graph: ManifestGraphModel,
  visibleTypes: Set<ManifestNodeType>,
): Promise<{ nodes: Node[]; edges: Edge[] }> {
  const filteredNodes = graph.nodes.filter((n) => visibleTypes.has(n.type))
  const filteredNodeIds = new Set(filteredNodes.map((n) => n.id))
  const filteredEdges = graph.edges.filter(
    (e) => filteredNodeIds.has(e.source) && filteredNodeIds.has(e.target),
  )

  if (filteredNodes.length === 0) {
    return { nodes: [], edges: [] }
  }

  const nodeMap = new Map(filteredNodes.map((n) => [n.id, n]))
  const connectedInstruments = countConnectedInstruments(filteredEdges)

  const layoutNodes = filteredNodes.map((n) => ({
    id: n.id,
    width: NODE_WIDTH,
    height: NODE_BASE_HEIGHT + NODE_PADDING,
  }))

  const rfEdges = buildReactFlowEdges(filteredEdges)

  const rfNodes = await layoutWithELK<ManifestNodeData>(
    layoutNodes,
    rfEdges,
    (id, position) => {
      const mn = nodeMap.get(id)!
      const color = NODE_TYPE_REGISTRY[mn.type].color
      return {
        id,
        type: mn.type,
        position,
        data: {
          manifestNode: mn,
          color,
          highlighted: false,
          dimmed: false,
          connectedInstrumentCount: connectedInstruments.get(id),
        } satisfies ManifestNodeData,
      }
    },
    {
      algorithm: 'force',
      nodeNodeSpacing: '80',
      extra: {
        'elk.force.iterations': '300',
        'elk.force.repulsion': '5.0',
      },
    },
  )

  // Assign optimal handle pairs based on laid-out node positions
  const posById = new Map(rfNodes.map((n) => [n.id, n.position]))
  for (const edge of rfEdges) {
    const srcPos = posById.get(edge.source)
    const tgtPos = posById.get(edge.target)
    if (srcPos && tgtPos) {
      const handles = pickHandles(srcPos, tgtPos)
      edge.sourceHandle = handles.sourceHandle
      edge.targetHandle = handles.targetHandle
    }
  }

  return { nodes: rfNodes, edges: rfEdges }
}
