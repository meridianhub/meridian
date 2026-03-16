import { useMemo } from 'react'
import { AlertCircle, RefreshCw, ShieldAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ManifestDiffGraph } from '@/features/manifests/components/manifest-diff-graph'
import { buildManifestGraph } from '@/features/manifests/lib/manifest-graph-model'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

export type ConflictResolution = 'force' | 'reload' | 'cancel'

export interface ConflictResolutionModalProps {
  open: boolean
  onResolve: (resolution: ConflictResolution) => void
  /** The manifest the user was trying to apply. */
  userManifest: Manifest
  /** The manifest currently on the server (newer version). */
  serverManifest: Manifest
}

export function ConflictResolutionModal({
  open,
  onResolve,
  userManifest,
  serverManifest,
}: ConflictResolutionModalProps) {
  const userGraph = useMemo(() => buildManifestGraph(userManifest), [userManifest])
  const serverGraph = useMemo(() => buildManifestGraph(serverManifest), [serverManifest])

  return (
    <Dialog open={open} onOpenChange={(isOpen) => { if (!isOpen) onResolve('cancel') }}>
      <DialogContent className="sm:max-w-3xl max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldAlert className="h-5 w-5 text-warning-foreground" />
            Version Conflict
          </DialogTitle>
          <DialogDescription>
            The manifest has been modified by another user since you last loaded it.
            Review the differences below and choose how to proceed.
          </DialogDescription>
        </DialogHeader>

        <div className="flex items-center gap-2 rounded-md border border-warning/30 bg-warning-muted px-3 py-2 text-sm text-warning-foreground">
          <AlertCircle className="h-4 w-4 shrink-0" />
          <span>
            Your changes conflict with the current server version.
            Forcing will overwrite the server version with your changes.
          </span>
        </div>

        <div className="min-h-[300px] flex-1 rounded-md border" data-testid="conflict-diff-graph">
          <ManifestDiffGraph
            before={serverGraph}
            after={userGraph}
            className="h-full"
          />
        </div>

        <DialogFooter className="flex-row gap-2 sm:justify-between">
          <Button
            variant="outline"
            size="sm"
            onClick={() => onResolve('cancel')}
            data-testid="conflict-cancel"
          >
            Cancel
          </Button>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => onResolve('reload')}
              data-testid="conflict-reload"
            >
              <RefreshCw className="h-4 w-4" />
              Reload Server Version
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => onResolve('force')}
              data-testid="conflict-force"
            >
              <ShieldAlert className="h-4 w-4" />
              Force Apply
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
