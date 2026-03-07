import type { ManifestGraph, ManifestNode, ManifestEdge } from './manifest-graph-model'

export interface ManifestDiff {
  addedNodes: ManifestNode[]
  removedNodes: ManifestNode[]
  modifiedNodes: Array<{ before: ManifestNode; after: ManifestNode }>
  addedEdges: ManifestEdge[]
  removedEdges: ManifestEdge[]
}

function nodesEqual(a: ManifestNode, b: ManifestNode): boolean {
  if (a.label !== b.label) return false
  const aKeys = Object.keys(a.data).sort()
  const bKeys = Object.keys(b.data).sort()
  if (aKeys.length !== bKeys.length) return false
  for (let i = 0; i < aKeys.length; i++) {
    if (aKeys[i] !== bKeys[i]) return false
    if (JSON.stringify(a.data[aKeys[i]]) !== JSON.stringify(b.data[bKeys[i]])) return false
  }
  return true
}

export function computeManifestDiff(before: ManifestGraph, after: ManifestGraph): ManifestDiff {
  const beforeNodeMap = new Map(before.nodes.map((n) => [n.id, n]))
  const afterNodeMap = new Map(after.nodes.map((n) => [n.id, n]))

  const addedNodes: ManifestNode[] = []
  const removedNodes: ManifestNode[] = []
  const modifiedNodes: Array<{ before: ManifestNode; after: ManifestNode }> = []

  for (const node of after.nodes) {
    const prev = beforeNodeMap.get(node.id)
    if (!prev) {
      addedNodes.push(node)
    } else if (!nodesEqual(prev, node)) {
      modifiedNodes.push({ before: prev, after: node })
    }
  }

  for (const node of before.nodes) {
    if (!afterNodeMap.has(node.id)) {
      removedNodes.push(node)
    }
  }

  const beforeEdgeIds = new Set(before.edges.map((e) => e.id))
  const afterEdgeIds = new Set(after.edges.map((e) => e.id))

  const addedEdges = after.edges.filter((e) => !beforeEdgeIds.has(e.id))
  const removedEdges = before.edges.filter((e) => !afterEdgeIds.has(e.id))

  return { addedNodes, removedNodes, modifiedNodes, addedEdges, removedEdges }
}
