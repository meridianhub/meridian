import { useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import { ManifestGraph } from '@/features/manifests/components/manifest-graph'
import { ManifestHistoryTable } from '@/features/manifests/pages/manifest-history-table'
import { Breadcrumbs } from '@/shared/breadcrumbs'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { Edit, Compass } from 'lucide-react'

function LoadingSkeleton() {
  return (
    <div data-testid="overview-loading" className="p-6 space-y-6">
      <div className="flex items-start justify-between">
        <div className="space-y-2">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-4 w-96" />
          <Skeleton className="h-5 w-20" />
        </div>
        <div className="flex gap-2">
          <Skeleton className="h-9 w-24" />
          <Skeleton className="h-9 w-32" />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-24" />
        ))}
      </div>
      <Skeleton className="h-[480px] w-full" />
      <Skeleton className="h-64 w-full" />
    </div>
  )
}

function EmptyState() {
  const navigate = useNavigate()
  return (
    <div data-testid="overview-empty" className="p-6 flex flex-col items-center gap-4 py-16 text-muted-foreground">
      <span className="text-lg font-medium">No economy configured</span>
      <span className="text-sm text-center max-w-md">
        Apply a manifest to configure instruments, account types, sagas, and more.
      </span>
      <Button onClick={() => navigate('/economy/edit')}>Configure Economy</Button>
    </div>
  )
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  return (
    <div data-testid="overview-error" className="p-6 flex flex-col items-center gap-3 py-16 text-muted-foreground">
      <span className="text-sm font-medium">Unable to load economy</span>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

interface StatChipProps {
  label: string
  value: number
  testId: string
  onClick?: () => void
}

function StatChip({ label, value, testId, onClick }: StatChipProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex items-center gap-2 rounded-lg border bg-card px-4 py-2.5 text-left transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
        {label}
      </span>
      <span className="text-lg font-bold" data-testid={testId}>
        {value}
      </span>
    </button>
  )
}

export function EconomyOverviewPage() {
  const { manifestHistory } = useApiClients()
  const navigate = useNavigate()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })

  const scrollToGraph = useCallback(() => {
    document.getElementById('relationship-graph')?.scrollIntoView({ behavior: 'smooth' })
  }, [])

  const isNotFound = error instanceof ConnectError && error.code === Code.NotFound

  const content = (() => {
    if (isLoading) return <LoadingSkeleton />
    if (error && !isNotFound) return <ErrorState onRetry={() => void refetch()} />
    if (isNotFound || !data?.version?.manifest) return <EmptyState />

    const { manifest } = data.version
    const metadata = manifest.metadata
    const instruments = manifest.instruments ?? []
    const accountTypes = manifest.accountTypes ?? []
    const sagas = manifest.sagas ?? []
    const valuationRules = manifest.valuationRules ?? []

    return (
      <>
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold">
            {metadata?.name ?? 'Economy'}
          </h1>
          {metadata?.description && (
            <p className="text-sm text-muted-foreground">{metadata.description}</p>
          )}
          {metadata?.industry && (
            <Badge variant="secondary" className="mt-1">
              {metadata.industry}
            </Badge>
          )}
        </div>
        <div className="flex gap-2 shrink-0">
          <Button
            variant="outline"
            onClick={() => navigate('/economy/explore')}
          >
            <Compass className="mr-2 size-4" />
            Explore
          </Button>
          <Button onClick={() => navigate('/economy/edit')}>
            <Edit className="mr-2 size-4" />
            Edit Economy
          </Button>
        </div>
      </div>

      {/* Stats - compact inline bar */}
      <div className="flex flex-wrap gap-3" data-testid="stats-bar">
        <StatChip label="Instruments" value={instruments.length} testId="stat-instruments" onClick={() => navigate('/reference-data/instruments')} />
        <StatChip label="Account Types" value={accountTypes.length} testId="stat-account-types" onClick={() => navigate('/reference-data/account-types')} />
        <StatChip label="Sagas" value={sagas.length} testId="stat-sagas" onClick={() => navigate('/starlark-config')} />
        <StatChip label="Valuation Rules" value={valuationRules.length} testId="stat-valuation-rules" onClick={scrollToGraph} />
      </div>

      {/* Relationship graph */}
      <section id="relationship-graph" className="space-y-3">
        <h2 className="text-base font-semibold">Relationship Graph</h2>
        <div className="h-[480px] rounded-lg border overflow-hidden">
          <ManifestGraph manifest={manifest} className="h-full w-full" />
        </div>
      </section>

      {/* Version history */}
      <section className="space-y-3">
        <h2 className="text-base font-semibold">Version History</h2>
        <ManifestHistoryTable />
      </section>
      </>
    )
  })()

  return (
    <div className="p-6 space-y-8">
      <Breadcrumbs items={[{ label: 'Economy' }]} />
      {content}
    </div>
  )
}
