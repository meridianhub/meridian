import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import { ManifestGraph } from '@/features/manifests/components/manifest-graph'
import { ManifestHistoryTable } from '@/features/manifests/pages/manifest-history-table'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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

interface StatCardProps {
  label: string
  value: number
  testId: string
}

function StatCard({ label, value, testId }: StatCardProps) {
  return (
    <Card>
      <CardHeader className="pb-1 pt-4 px-4">
        <CardTitle className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          {label}
        </CardTitle>
      </CardHeader>
      <CardContent className="px-4 pb-4">
        <span className="text-3xl font-bold" data-testid={testId}>
          {value}
        </span>
      </CardContent>
    </Card>
  )
}

export function EconomyOverviewPage() {
  const { manifestHistory } = useApiClients()
  const navigate = useNavigate()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })

  const isNotFound = error instanceof ConnectError && error.code === Code.NotFound

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
    <div className="p-6 space-y-8">
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

      {/* Stats */}
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <StatCard label="Instruments" value={instruments.length} testId="stat-instruments" />
        <StatCard label="Account Types" value={accountTypes.length} testId="stat-account-types" />
        <StatCard label="Sagas" value={sagas.length} testId="stat-sagas" />
        <StatCard label="Valuation Rules" value={valuationRules.length} testId="stat-valuation-rules" />
      </div>

      {/* Relationship graph */}
      <section className="space-y-3">
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
    </div>
  )
}
