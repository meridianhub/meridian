import { useState } from 'react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ManifestCurrentView } from './manifest-current-view'
import { ManifestHistoryTable } from './manifest-history-table'
import { ManifestApplyDialog } from './manifest-apply-dialog'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'

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
        </TabsList>
        <TabsContent value="current">
          <ManifestCurrentView />
        </TabsContent>
        <TabsContent value="history">
          <ManifestHistoryTable />
        </TabsContent>
      </Tabs>

      <ManifestApplyDialog
        open={applyDialogOpen}
        onOpenChange={setApplyDialogOpen}
      />
    </div>
  )
}
