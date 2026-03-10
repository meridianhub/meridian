import { cn } from '@/lib/utils'
import { ManifestGraph } from '@/features/manifests/components/manifest-graph'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

interface EditorGraphPanelProps {
  manifest: Manifest | null
  validationPassed: boolean
  className?: string
}

export function EditorGraphPanel({
  manifest,
  validationPassed,
  className,
}: EditorGraphPanelProps) {
  if (!manifest) {
    return (
      <div
        className={cn(
          'flex items-center justify-center rounded-lg border border-dashed bg-muted/20 text-sm text-muted-foreground',
          className,
        )}
      >
        No valid manifest to visualize
      </div>
    )
  }

  return (
    <div className={cn('relative rounded-lg border', className)}>
      {!validationPassed && (
        <div className="absolute inset-0 z-10 flex items-center justify-center rounded-lg bg-background/60 backdrop-blur-[1px]">
          <span className="rounded-md bg-muted px-3 py-1.5 text-xs font-medium text-muted-foreground">
            Graph stale -- fix validation errors to update
          </span>
        </div>
      )}
      <div
        className={cn(!validationPassed && 'pointer-events-none opacity-50')}
      >
        <ManifestGraph manifest={manifest} className="h-full w-full" />
      </div>
    </div>
  )
}
