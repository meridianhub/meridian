import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'

const DEFAULT_POLL_INTERVAL = 30_000 // 30 seconds

interface UseReconcileManifestOptions {
  pollInterval?: number
  enabled?: boolean
}

interface ReconcileResult {
  driftDetected: boolean
  isLoading: boolean
  currentVersion?: string
  lastCheckedAt?: Date
}

/**
 * Polls the current manifest to detect drift.
 * Compares the manifest version/appliedAt between fetches.
 * When a reconcileManifest RPC is available, this hook should call it instead.
 */
export function useReconcileManifest(options: UseReconcileManifestOptions = {}): ReconcileResult {
  const { pollInterval = DEFAULT_POLL_INTERVAL, enabled = true } = options
  const { manifestHistory } = useApiClients()

  const { data, isLoading } = useQuery({
    queryKey: [...manifestKeys.all, 'reconcile'] as const,
    queryFn: async () => {
      const response = await manifestHistory.getCurrentManifest({})
      return {
        version: response.version?.version,
        appliedAt: response.version?.appliedAt
          ? new Date(Number(response.version.appliedAt.seconds) * 1000)
          : undefined,
        checkedAt: new Date(),
      }
    },
    refetchInterval: pollInterval,
    enabled,
  })

  // Drift detection: currently a placeholder.
  // When the ReconcileManifest RPC is implemented, this will compare
  // declared state vs actual system state. For now, no drift is detected.
  return {
    driftDetected: false,
    isLoading,
    currentVersion: data?.version,
    lastCheckedAt: data?.checkedAt,
  }
}
