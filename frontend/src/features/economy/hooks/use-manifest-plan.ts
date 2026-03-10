import { useMutation } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import type {
  ApplyManifestResponse,
  StepResult,
  ValidationError,
} from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import { ApplyManifestStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

export interface ManifestPlan {
  /** The raw response status. */
  status: ApplyManifestStatus
  /** Human-readable diff summary from the server. */
  diffSummary: string
  /** Individual step results from the dry-run. */
  stepResults: StepResult[]
  /** Validation errors encountered during planning. */
  validationErrors: ValidationError[]
  /** Counts parsed from the diff summary. */
  counts: {
    add: number
    modify: number
    remove: number
  }
}

function parseDiffCounts(diffSummary: string): { add: number; modify: number; remove: number } {
  const add = parseInt(diffSummary.match(/(\d+)\s*add/i)?.[1] ?? '0', 10)
  const modify = parseInt(diffSummary.match(/(\d+)\s*modif/i)?.[1] ?? '0', 10)
  const remove = parseInt(diffSummary.match(/(\d+)\s*(?:remove|delet)/i)?.[1] ?? '0', 10)
  return { add, modify, remove }
}

function toManifestPlan(response: ApplyManifestResponse): ManifestPlan {
  return {
    status: response.status,
    diffSummary: response.diffSummary,
    stepResults: response.stepResults,
    validationErrors: response.validationErrors,
    counts: parseDiffCounts(response.diffSummary),
  }
}

export function useManifestPlan() {
  const { manifestApplier } = useApiClients()

  const mutation = useMutation({
    mutationFn: async (manifest: Manifest): Promise<ManifestPlan> => {
      const response = await manifestApplier.applyManifest({
        manifest,
        dryRun: true,
        force: false,
        appliedBy: '',
      })
      return toManifestPlan(response)
    },
  })

  return {
    plan: mutation.data ?? null,
    planManifest: mutation.mutate,
    planManifestAsync: mutation.mutateAsync,
    isPlanning: mutation.isPending,
    error: mutation.error,
  }
}
