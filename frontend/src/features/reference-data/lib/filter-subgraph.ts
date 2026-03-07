import type {
  ManifestGraph,
  ManifestNode,
  ManifestEdge,
} from '@/features/manifests/lib/manifest-graph-model'

export function filterSubgraph(
  graph: ManifestGraph,
  focusNodeId: string,
): { nodes: ManifestNode[]; edges: ManifestEdge[] } {
  const connectedNodeIds = new Set<string>()
  connectedNodeIds.add(focusNodeId)

  for (const edge of graph.edges) {
    if (edge.source === focusNodeId || edge.target === focusNodeId) {
      connectedNodeIds.add(edge.source)
      connectedNodeIds.add(edge.target)
    }
  }

  // Also include sagas that write to any connected node
  for (const edge of graph.edges) {
    if (edge.relationship === 'writes_to' && connectedNodeIds.has(edge.target)) {
      connectedNodeIds.add(edge.source)
    }
  }

  const filteredNodes = graph.nodes.filter((n) => connectedNodeIds.has(n.id))
  const filteredEdges = graph.edges.filter(
    (e) => connectedNodeIds.has(e.source) && connectedNodeIds.has(e.target),
  )

  return { nodes: filteredNodes, edges: filteredEdges }
}
