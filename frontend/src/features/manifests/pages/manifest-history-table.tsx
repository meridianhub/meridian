import { useMemo, useState } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable, type DataTableQueryParams, type DataTableResult } from '@/shared/data-table'
import { useApiClients } from '@/api/context'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { manifestKeys } from '@/lib/query-keys'
import type { ManifestVersion } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { buildManifestGraph } from '../lib/manifest-graph-model'
import { ManifestDiffGraph } from '../components/manifest-diff-graph'

const APPLY_STATUS_LABEL: Record<number, string> = {
  [ApplyStatus.APPLIED]: 'APPLIED',
  [ApplyStatus.FAILED]: 'FAILED',
  [ApplyStatus.ROLLED_BACK]: 'ROLLED_BACK',
}

function buildColumns(
  compareSet: Set<string>,
  onToggleCompare: (version: ManifestVersion) => void,
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
  ]
}

export function ManifestHistoryTable() {
  const { manifestHistory } = useApiClients()
  const [selectedVersion, setSelectedVersion] = useState<ManifestVersion | null>(null)
  const [compareVersions, setCompareVersions] = useState<ManifestVersion[]>([])
  const [showDiff, setShowDiff] = useState(false)

  const compareSet = useMemo(
    () => new Set(compareVersions.map((v) => v.id ?? v.version)),
    [compareVersions],
  )

  function toggleCompare(version: ManifestVersion) {
    const versionId = version.id ?? version.version
    setCompareVersions((prev) => {
      const exists = prev.find((v) => (v.id ?? v.version) === versionId)
      if (exists) return prev.filter((v) => (v.id ?? v.version) !== versionId)
      if (prev.length >= 2) return prev
      return [...prev, version]
    })
  }

  const columns = useMemo(
    () => buildColumns(compareSet, toggleCompare),
    [compareSet],
  )

  async function fetchVersions(params: DataTableQueryParams): Promise<DataTableResult<ManifestVersion>> {
    const parsed = params.pageToken ? parseInt(params.pageToken, 10) : 0
    const offset = Number.isNaN(parsed) ? 0 : parsed
    const response = await manifestHistory.listManifestVersions({
      limit: params.pageSize,
      offset,
    })
    const nextOffset = offset + params.pageSize
    return {
      items: response.versions ?? [],
      nextPageToken: nextOffset < response.totalCount ? String(nextOffset) : undefined,
    }
  }

  function handleCompare() {
    if (compareVersions.length === 2) {
      setShowDiff(true)
    }
  }

  function closeDiff() {
    setShowDiff(false)
  }

  const diffGraphs = useMemo(() => {
    if (!showDiff || compareVersions.length !== 2) return null
    // Sort by appliedAt so earlier version is always "before"
    const sorted = [...compareVersions].sort((a, b) => {
      const aTime = Number(a.appliedAt?.seconds ?? 0n)
      const bTime = Number(b.appliedAt?.seconds ?? 0n)
      return aTime - bTime
    })
    const [v1, v2] = sorted
    if (!v1.manifest || !v2.manifest) return null
    const before = buildManifestGraph(v1.manifest as unknown as Manifest)
    const after = buildManifestGraph(v2.manifest as unknown as Manifest)
    return { before, after, v1Label: v1.version, v2Label: v2.version }
  }, [showDiff, compareVersions])

  return (
    <>
      <div data-testid="manifest-history-table">
        {compareVersions.length > 0 && (
          <div className="flex items-center gap-3 pb-3" data-testid="compare-toolbar">
            <span className="text-sm text-muted-foreground">
              {compareVersions.length}/2 versions selected
            </span>
            <Button
              size="sm"
              disabled={compareVersions.length !== 2}
              onClick={handleCompare}
              data-testid="compare-button"
            >
              Compare
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setCompareVersions([])}
            >
              Clear
            </Button>
          </div>
        )}
        <DataTable<ManifestVersion>
          queryKey={manifestKeys.history()}
          queryFn={fetchVersions}
          columns={columns}
          pageSize={20}
          onRowClick={setSelectedVersion}
        />
      </div>

      <Dialog open={selectedVersion != null} onOpenChange={() => setSelectedVersion(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>Manifest Version {selectedVersion?.version}</DialogTitle>
          </DialogHeader>
          <pre className="max-h-[400px] overflow-auto rounded bg-muted p-3 text-xs font-mono">
            {JSON.stringify(selectedVersion?.manifest, null, 2)}
          </pre>
        </DialogContent>
      </Dialog>

      <Dialog open={showDiff} onOpenChange={closeDiff}>
        <DialogContent className="max-w-5xl h-[80vh]">
          <DialogHeader>
            <DialogTitle>
              Diff: Version {diffGraphs?.v1Label} vs {diffGraphs?.v2Label}
            </DialogTitle>
          </DialogHeader>
          <div className="flex-1 min-h-0 h-full">
            {diffGraphs && (
              <ManifestDiffGraph
                before={diffGraphs.before}
                after={diffGraphs.after}
                className="h-full"
              />
            )}
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
