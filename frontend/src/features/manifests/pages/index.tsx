import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Skeleton } from '@/components/ui/skeleton'
import { ManifestCurrentView } from './manifest-current-view'
import { ManifestHistoryTable } from './manifest-history-table'
import { ManifestApplyDialog } from './manifest-apply-dialog'
import { ManifestGraph } from '../components/manifest-graph'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import { manifestKeys } from '@/lib/query-keys'

function ManifestGraphTab() {
  const { manifestHistory } = useApiClients()

  const { data, isLoading, error } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })

  if (isLoading) {
    return (
      <div data-testid="graph-loading" className="space-y-4">
        <Skeleton className="h-[500px] w-full" />
      </div>
    )
  }

  if (error) {
    return (
      <div data-testid="graph-error" className="flex flex-col items-center gap-2 py-8 text-muted-foreground">
        <span className="text-sm font-medium">Unable to load manifest graph</span>
        {error instanceof Error && error.message && (
          <span className="text-xs max-w-md text-center">{error.message}</span>
        )}
      </div>
    )
  }

  const manifest = data?.version?.manifest
  if (!manifest) {
    return (
      <div data-testid="graph-empty" className="flex flex-col items-center gap-2 py-8 text-muted-foreground">
        <span className="text-sm font-medium">No manifest applied</span>
        <span className="text-xs">Apply a manifest to see the graph visualization.</span>
      </div>
    )
  }

  return <ManifestGraph manifest={manifest} />
}

export function ManifestsPage() {
  const [applyDialogOpen, setApplyDialogOpen] = useState(false)

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Manifest Configuration</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            View and manage tenant manifest configuration
          </p>
        </div>
        <Button onClick={() => setApplyDialogOpen(true)}>
          <Plus className="mr-2 size-4" />
          Apply Manifest
        </Button>
      </div>

      <Tabs defaultValue="current">
        <TabsList>
          <TabsTrigger value="current">Current Manifest</TabsTrigger>
          <TabsTrigger value="history">Version History</TabsTrigger>
          <TabsTrigger value="graph">Graph</TabsTrigger>
        </TabsList>
        <TabsContent value="current">
          <ManifestCurrentView />
        </TabsContent>
        <TabsContent value="history">
          <ManifestHistoryTable />
        </TabsContent>
        <TabsContent value="graph">
          <ManifestGraphTab />
        </TabsContent>
      </Tabs>

      <ManifestApplyDialog
        open={applyDialogOpen}
        onOpenChange={setApplyDialogOpen}
      />
    </div>
  )
}
