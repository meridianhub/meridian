import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { manifestKeys } from '@/lib/query-keys'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

const APPLY_STATUS_LABEL: Record<number, string> = {
  [ApplyStatus.APPLIED]: 'APPLIED',
  [ApplyStatus.FAILED]: 'FAILED',
  [ApplyStatus.ROLLED_BACK]: 'ROLLED_BACK',
}

function LoadingSkeleton() {
  return (
    <div data-testid="loading-skeleton" className="space-y-4">
      <Skeleton className="h-6 w-48" />
      <Skeleton className="h-4 w-32" />
      <Skeleton className="h-24 w-full" />
    </div>
  )
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 py-8 text-muted-foreground">
      <span className="text-sm font-medium">Failed to load manifest</span>
      <span className="text-xs">The manifest history service may not be available for this environment.</span>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

function EmptyState() {
  return (
    <div data-testid="empty-state" className="flex flex-col items-center gap-2 py-8 text-muted-foreground">
      <span className="text-sm font-medium">No manifest applied</span>
      <span className="text-xs">Apply a manifest to get started.</span>
    </div>
  )
}

interface ExpandableSectionProps {
  title: string
  count: number
  children: React.ReactNode
  'data-testid'?: string
}

function ExpandableSection({ title, count, children, 'data-testid': testId }: ExpandableSectionProps) {
  const [open, setOpen] = useState(false)

  return (
    <div className="border-b border-border last:border-b-0" data-testid={testId}>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-2 px-4 py-2 text-sm font-medium hover:bg-muted/50"
        aria-expanded={open}
      >
        {open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
        {title} ({count})
      </button>
      {open && <div className="px-4 pb-3">{children}</div>}
    </div>
  )
}

export function ManifestCurrentView() {
  const { manifestHistory } = useApiClients()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })

  if (isLoading) return <LoadingSkeleton />
  if (error) return <ErrorState onRetry={() => void refetch()} />
  if (!data?.version) return <EmptyState />

  const { version, manifest, appliedAt, appliedBy, applyStatus } = data.version
  const statusLabel = APPLY_STATUS_LABEL[applyStatus] ?? 'UNKNOWN'

  const instruments = manifest?.instruments ?? []
  const accountTypes = manifest?.accountTypes ?? []
  const valuationRules = manifest?.valuationRules ?? []
  const sagas = manifest?.sagas ?? []

  return (
    <Card className="overflow-hidden" data-testid="manifest-current-view">
      <div className="border-b border-border px-4 py-3">
        <div className="flex items-center gap-3">
          <h3 className="text-lg font-semibold">Version {version}</h3>
          <StatusBadge status={statusLabel} />
        </div>
        <div className="mt-1 flex items-center gap-4 text-sm text-muted-foreground">
          <span>Applied by {appliedBy}</span>
          <TimeDisplay timestamp={appliedAt} />
        </div>
        {manifest?.metadata && (
          <div className="mt-2 text-sm text-muted-foreground">
            <span className="font-medium">{manifest.metadata.name}</span>
            {manifest.metadata.industry && (
              <span className="ml-2 text-xs">({manifest.metadata.industry})</span>
            )}
          </div>
        )}
      </div>

      <ExpandableSection title="Instruments" count={instruments.length} data-testid="instruments-section">
        {instruments.length === 0 ? (
          <p className="text-xs text-muted-foreground">No instruments defined.</p>
        ) : (
          <ul className="space-y-1 text-xs">
            {instruments.map((inst) => (
              <li key={inst.code} className="flex items-center gap-2">
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{inst.code}</code>
                <span>{inst.name}</span>
                {inst.dimensions?.unit && (
                  <span className="text-muted-foreground">({inst.dimensions.unit})</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </ExpandableSection>

      <ExpandableSection title="Account Types" count={accountTypes.length} data-testid="account-types-section">
        {accountTypes.length === 0 ? (
          <p className="text-xs text-muted-foreground">No account types defined.</p>
        ) : (
          <ul className="space-y-1 text-xs">
            {accountTypes.map((at) => (
              <li key={at.code} className="flex items-center gap-2">
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{at.code}</code>
                <span>{at.name}</span>
              </li>
            ))}
          </ul>
        )}
      </ExpandableSection>

      <ExpandableSection title="Valuation Rules" count={valuationRules.length}>
        {valuationRules.length === 0 ? (
          <p className="text-xs text-muted-foreground">No valuation rules defined.</p>
        ) : (
          <ul className="space-y-1 text-xs">
            {valuationRules.map((rule, i) => (
              <li key={i} className="flex items-center gap-2">
                <code className="font-mono">{rule.fromInstrument}</code>
                <span>-&gt;</span>
                <code className="font-mono">{rule.toInstrument}</code>
              </li>
            ))}
          </ul>
        )}
      </ExpandableSection>

      <ExpandableSection title="Sagas" count={sagas.length}>
        {sagas.length === 0 ? (
          <p className="text-xs text-muted-foreground">No sagas defined.</p>
        ) : (
          <ul className="space-y-1 text-xs">
            {sagas.map((saga) => (
              <li key={saga.name} className="flex items-center gap-2">
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{saga.name}</code>
                <span className="text-muted-foreground">{saga.trigger}</span>
              </li>
            ))}
          </ul>
        )}
      </ExpandableSection>
    </Card>
  )
}
