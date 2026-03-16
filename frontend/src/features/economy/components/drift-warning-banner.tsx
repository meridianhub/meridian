import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useReconcileManifest } from '@/features/economy/hooks/use-reconcile-manifest'

interface DriftWarningBannerProps {
  pollInterval?: number
}

export function DriftWarningBanner({ pollInterval }: DriftWarningBannerProps) {
  const { driftDetected, currentVersion, lastCheckedAt } = useReconcileManifest({ pollInterval })

  if (!driftDetected) return null

  return (
    <div
      data-testid="drift-warning-banner"
      className="flex items-center gap-3 rounded-lg border border-yellow-300 bg-yellow-50 px-4 py-3 text-yellow-800 dark:border-yellow-700 dark:bg-yellow-950 dark:text-yellow-200"
      role="alert"
    >
      <AlertTriangle className="size-5 shrink-0" />
      <div className="flex-1 text-sm">
        <span className="font-medium">Manifest drift detected.</span>{' '}
        The declared manifest (v{currentVersion}) differs from the actual system state.
        {lastCheckedAt && (
          <span className="text-xs text-yellow-600 dark:text-yellow-400 ml-1">
            Last checked: {lastCheckedAt.toLocaleTimeString()}
          </span>
        )}
      </div>
      <Button variant="outline" size="sm" className="shrink-0" disabled>
        Reconcile Now
      </Button>
    </div>
  )
}
