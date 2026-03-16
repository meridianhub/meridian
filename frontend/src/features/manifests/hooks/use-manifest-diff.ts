import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import type {
  DiffManifestVersionsResponse,
} from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

export function useManifestDiff(baseSeq: number, targetSeq: number): {
  data: DiffManifestVersionsResponse | undefined
  isLoading: boolean
  error: Error | null
} {
  const { manifestHistory } = useApiClients()

  const { data, isLoading, error } = useQuery({
    queryKey: manifestKeys.diff(baseSeq, targetSeq),
    queryFn: () =>
      manifestHistory.diffManifestVersions({
        baseSequenceNumber: BigInt(baseSeq),
        targetSequenceNumber: BigInt(targetSeq),
      }),
    enabled: targetSeq > 0,
  })

  return { data, isLoading, error: error as Error | null }
}
