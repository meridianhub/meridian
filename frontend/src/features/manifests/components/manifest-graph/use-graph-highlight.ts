import { useEffect, type Dispatch, type SetStateAction } from 'react'
import type { Node, Edge } from '@xyflow/react'
import type { ManifestEdge } from '../../lib/manifest-graph-model'
import type { ManifestNodeData } from './node-renderers'

interface GraphHighlightArgs {
  hoveredNode: string | null
  selectedNode: string | null
  currentEdges: ManifestEdge[]
  setNodes: Dispatch<SetStateAction<Node[]>>
  setEdges: Dispatch<SetStateAction<Edge[]>>
}

/** Collect the active node plus all of its directly connected neighbours. */
function connectedNodeIds(activeNode: string | null, edges: ManifestEdge[]): Set<string> {
  const connected = new Set<string>()
  if (!activeNode) return connected
  connected.add(activeNode)
  for (const e of edges) {
    if (e.source === activeNode || e.target === activeNode) {
      connected.add(e.source)
      connected.add(e.target)
    }
  }
  return connected
}

/**
 * Drive node dim/highlight state and edge animation from the currently hovered
 * or selected node. Updates are applied in place only when something changed to
 * avoid redundant React Flow re-renders.
 */
export function useGraphHighlight({
  hoveredNode,
  selectedNode,
  currentEdges,
  setNodes,
  setEdges,
}: GraphHighlightArgs): void {
  useEffect(() => {
    const activeNode = hoveredNode ?? selectedNode
    const connectedNodes = connectedNodeIds(activeNode, currentEdges)

    setNodes((nds) => {
      let changed = false
      const next = nds.map((n) => {
        const highlighted = activeNode ? n.id === activeNode : false
        const dimmed = activeNode ? !connectedNodes.has(n.id) : false
        const current = n.data as ManifestNodeData
        if (current.highlighted === highlighted && current.dimmed === dimmed) return n
        changed = true
        return { ...n, data: { ...n.data, highlighted, dimmed } }
      })
      return changed ? next : nds
    })

    setEdges((eds) => {
      let changed = false
      const next = eds.map((e) => {
        const animated = hoveredNode ? e.source === hoveredNode || e.target === hoveredNode : false
        if (e.animated === animated) return e
        changed = true
        return { ...e, animated }
      })
      return changed ? next : eds
    })
  }, [hoveredNode, selectedNode, currentEdges, setNodes, setEdges])
}
