import type { ColumnDef } from '@tanstack/react-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { Button } from '@/components/ui/button'
import { RotateCcw } from 'lucide-react'
import type { ManifestVersion } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

const APPLY_STATUS_LABEL: Record<number, string> = {
  [ApplyStatus.APPLIED]: 'APPLIED',
  [ApplyStatus.FAILED]: 'FAILED',
  [ApplyStatus.ROLLED_BACK]: 'ROLLED_BACK',
  [ApplyStatus.PARTIAL]: 'PARTIAL',
}

export function buildColumns(
  compareSet: Set<string>,
  onToggleCompare: (version: ManifestVersion) => void,
  onRollback: (version: ManifestVersion) => void,
): ColumnDef<ManifestVersion>[] {
  return [
    {
      id: 'compare',
      header: 'Compare',
      cell: ({ row }) => {
        const v = row.original
        const versionId = v.id ?? v.version
        const isSelected = compareSet.has(versionId)
        const disabled = !isSelected && compareSet.size >= 2
        return (
          <input
            type="checkbox"
            checked={isSelected}
            disabled={disabled}
            onChange={() => onToggleCompare(v)}
            onClick={(e) => e.stopPropagation()}
            aria-label={`Select version ${v.version} for comparison`}
            className="rounded"
            data-testid={`compare-checkbox-${v.version}`}
          />
        )
      },
      enableSorting: false,
    },
    {
      accessorKey: 'version',
      header: 'Version',
    },
    {
      accessorKey: 'appliedAt',
      header: 'Applied At',
      cell: ({ row }) => <TimeDisplay timestamp={row.original.appliedAt} />,
    },
    {
      accessorKey: 'appliedBy',
      header: 'Applied By',
    },
    {
      accessorKey: 'applyStatus',
      header: 'Status',
      cell: ({ row }) => {
        const label = APPLY_STATUS_LABEL[row.original.applyStatus] ?? 'UNKNOWN'
        return <StatusBadge status={label} />
      },
    },
    {
      accessorKey: 'diffSummary',
      header: 'Changes',
      cell: ({ row }) => (
        <span className="text-sm text-muted-foreground">
          {row.original.diffSummary ?? '\u2014'}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={(e) => {
            e.stopPropagation()
            onRollback(row.original)
          }}
          title={`Rollback to version ${row.original.version}`}
          data-testid={`rollback-button-${row.original.version}`}
        >
          <RotateCcw className="h-4 w-4" />
        </Button>
      ),
      enableSorting: false,
    },
  ]
}
