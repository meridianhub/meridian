import { useEffect, type Dispatch, type SetStateAction } from 'react'
import type { Node, Edge } from '@xyflow/react'
import type { ManifestGraph, ManifestNodeType } from '../../lib/manifest-graph-model'
import { layoutManifestGraph } from './graph-layout'

/**
 * Recompute the ELK layout whenever the graph or visible types change and push
 * the resulting nodes/edges into React Flow state. Stale async results are
 * dropped via a cancellation flag.
 */
export function useGraphLayoutEffect(
  graph: ManifestGraph,
  visibleTypes: Set<ManifestNodeType>,
  setNodes: Dispatch<SetStateAction<Node[]>>,
  setEdges: Dispatch<SetStateAction<Edge[]>>,
): void {
  useEffect(() => {
    let cancelled = false
    void layoutManifestGraph(graph, visibleTypes)
      .then((result) => {
        if (!cancelled) {
          setNodes(result.nodes)
          setEdges(result.edges)
        }
      })
      .catch((err) => {
        if (!cancelled) {
          console.error('[ManifestGraph] layout failed:', err)
        }
      })
    return () => {
      cancelled = true
    }
  }, [graph, visibleTypes, setNodes, setEdges])
}
