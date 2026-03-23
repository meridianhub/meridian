import { useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import type { ManifestVersion } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { RollbackStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { useRollbackManifest } from '../hooks/use-rollback-manifest'
import { TimeDisplay } from '@/shared/time-display'

interface RollbackConfirmationDialogProps {
  version: ManifestVersion | null
  open: boolean
  onOpenChange: (open: boolean) => void
  appliedBy: string
}

export function RollbackConfirmationDialog({
  version,
  open,
  onOpenChange,
  appliedBy,
}: RollbackConfirmationDialogProps) {
  const rollback = useRollbackManifest()
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewMessage, setPreviewMessage] = useState<string | null>(null)

  async function handlePreview() {
    if (!version) return
    setPreviewLoading(true)
    setPreviewMessage(null)
    try {
      const result = await rollback.mutateAsync({
        targetSequenceNumber: BigInt(version.sequenceNumber),
        dryRun: true,
        appliedBy,
      })
      setPreviewMessage(result.message || 'Preview complete')
    } catch (err) {
      setPreviewMessage(`Preview failed: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setPreviewLoading(false)
    }
  }

  async function handleRollback() {
    if (!version) return
    try {
      const result = await rollback.mutateAsync({
        targetSequenceNumber: BigInt(version.sequenceNumber),
        dryRun: false,
        appliedBy,
      })
      if (result.status === RollbackStatus.COMPLETED) {
        onOpenChange(false)
        setPreviewMessage(null)
      }
    } catch {
      // Error is available via rollback.error
    }
  }

  function handleClose(open: boolean) {
    if (!open) {
      setPreviewMessage(null)
      rollback.reset()
    }
    onOpenChange(open)
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Rollback Manifest</DialogTitle>
          <DialogDescription>
            Revert to version {version?.version} (sequence #{String(version?.sequenceNumber ?? '')}).
            This creates a new version record - history is preserved.
          </DialogDescription>
        </DialogHeader>

        {version && (
          <div className="space-y-2 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">Version</span>
              <span className="font-mono">{version.version}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Originally applied</span>
              <TimeDisplay timestamp={version.appliedAt} />
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Applied by</span>
              <span>{version.appliedBy}</span>
            </div>
          </div>
        )}

        {previewMessage && (
          <div className="rounded bg-muted p-3 text-sm" data-testid="rollback-preview">
            {previewMessage}
          </div>
        )}

        {rollback.error && (
          <div className="rounded bg-destructive/10 p-3 text-sm text-destructive" data-testid="rollback-error">
            {rollback.error.message}
          </div>
        )}

        <DialogFooter className="gap-2 sm:gap-0">
          <Button
            variant="outline"
            size="sm"
            onClick={handlePreview}
            disabled={previewLoading || rollback.isPending}
            data-testid="rollback-preview-button"
          >
            {previewLoading ? 'Previewing...' : 'Preview'}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={handleRollback}
            disabled={rollback.isPending}
            data-testid="rollback-confirm-button"
          >
            {rollback.isPending ? 'Rolling back...' : 'Rollback'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
