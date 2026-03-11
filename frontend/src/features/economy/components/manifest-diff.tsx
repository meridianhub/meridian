import { Plus, Minus, RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { ManifestDiff } from '@/features/manifests/lib/manifest-diff'
import type { ManifestNode } from '@/features/manifests/lib/manifest-graph-model'

interface ManifestDiffViewerProps {
  diff: ManifestDiff | null
  onNodeClick?: (node: ManifestNode) => void
  className?: string
}

export function ManifestDiffViewer({ diff, onNodeClick, className }: ManifestDiffViewerProps) {
  if (!diff) return null

  const hasChanges =
    diff.addedNodes.length > 0 || diff.removedNodes.length > 0 || diff.modifiedNodes.length > 0

  if (!hasChanges) {
    return (
      <div className={cn('py-4 text-center text-sm text-muted-foreground', className)}>
        No changes detected
      </div>
    )
  }

  return (
    <div className={cn('space-y-4', className)}>
      {diff.addedNodes.length > 0 && (
        <section>
          <SectionHeader
            icon={<Plus className="h-3.5 w-3.5" />}
            label="Added"
            count={diff.addedNodes.length}
            colorClass="text-green-600 dark:text-green-400"
          />
          <ul className="mt-1.5 space-y-1">
            {diff.addedNodes.map((node) => (
              <NodeRow
                key={node.id}
                label={node.label}
                colorClass="border-green-500/40 bg-green-50/60 dark:bg-green-950/30"
                onClick={onNodeClick ? () => onNodeClick(node) : undefined}
              />
            ))}
          </ul>
        </section>
      )}

      {diff.modifiedNodes.length > 0 && (
        <section>
          <SectionHeader
            icon={<RefreshCw className="h-3.5 w-3.5" />}
            label="Modified"
            count={diff.modifiedNodes.length}
            colorClass="text-amber-600 dark:text-amber-400"
          />
          <ul className="mt-1.5 space-y-2">
            {diff.modifiedNodes.map(({ before, after }) => (
              <li key={after.id} className="rounded border border-amber-500/40 bg-amber-50/60 px-3 py-2 text-sm dark:bg-amber-950/30">
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                  <span className="font-medium text-foreground/70">Before:</span>
                  <span>{before.label}</span>
                </div>
                <div className="mt-0.5 flex items-center gap-2 text-xs">
                  <span className="font-medium text-foreground/70">After:</span>
                  <span className="font-medium">{after.label}</span>
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}

      {diff.removedNodes.length > 0 && (
        <section>
          <SectionHeader
            icon={<Minus className="h-3.5 w-3.5" />}
            label="Removed"
            count={diff.removedNodes.length}
            colorClass="text-red-600 dark:text-red-400"
          />
          <ul className="mt-1.5 space-y-1">
            {diff.removedNodes.map((node) => (
              <NodeRow
                key={node.id}
                label={node.label}
                colorClass="border-red-500/40 bg-red-50/60 dark:bg-red-950/30"
                onClick={onNodeClick ? () => onNodeClick(node) : undefined}
              />
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

interface SectionHeaderProps {
  icon: React.ReactNode
  label: string
  count: number
  colorClass: string
}

function SectionHeader({ icon, label, count, colorClass }: SectionHeaderProps) {
  return (
    <div className={cn('flex items-center gap-1.5 text-sm font-medium', colorClass)}>
      {icon}
      <span>{label}</span>
      <span className="ml-auto rounded-full bg-muted px-1.5 py-0.5 text-xs font-normal text-foreground">
        {count}
      </span>
    </div>
  )
}

interface NodeRowProps {
  label: string
  colorClass: string
  onClick?: () => void
}

function NodeRow({ label, colorClass, onClick }: NodeRowProps) {
  return (
    <li
      className={cn(
        'rounded border px-3 py-1.5 text-sm',
        colorClass,
        onClick && 'cursor-pointer transition-opacity hover:opacity-80',
      )}
      onClick={onClick}
    >
      {label}
    </li>
  )
}
