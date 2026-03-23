import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import type { RollbackManifestResponse } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

interface RollbackParams {
  targetSequenceNumber: bigint
  dryRun: boolean
  appliedBy: string
}

export function useRollbackManifest() {
  const { manifestHistory } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation<RollbackManifestResponse, Error, RollbackParams>({
    mutationFn: (params) =>
      manifestHistory.rollbackManifest({
        targetSequenceNumber: params.targetSequenceNumber,
        dryRun: params.dryRun,
        appliedBy: params.appliedBy,
      }),
    onSuccess: (_data, variables) => {
      if (!variables.dryRun) {
        queryClient.invalidateQueries({ queryKey: manifestKeys.all })
      }
    },
  })
}
