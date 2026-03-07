import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import { buildManifestGraph, type ManifestGraph } from '../lib/manifest-graph-model'

export function useManifestGraph(): {
  graph: ManifestGraph | null
  isLoading: boolean
  error: Error | null
} {
  const { manifestHistory } = useApiClients()

  const { data, isLoading, error } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })

  const graph = useMemo(() => {
    const manifest = data?.version?.manifest
    if (!manifest) return null
    return buildManifestGraph(manifest)
  }, [data])

  return { graph, isLoading, error: error as Error | null }
}
