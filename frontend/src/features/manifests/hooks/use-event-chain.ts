import { useMemo } from 'react'
import { computeTransitiveClosure, type EventChain } from '../lib/transitive-closure'
import type { ManifestGraph } from '../lib/manifest-graph-model'

export function useEventChain(graph: ManifestGraph | null, nodeId: string | null): EventChain | null {
  return useMemo(() => {
    if (!graph || !nodeId) return null
    return computeTransitiveClosure(graph, nodeId)
  }, [graph, nodeId])
}
