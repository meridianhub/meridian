import { useState } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable, type DataTableQueryParams, type DataTableResult } from '@/components/shared/data-table'
import { useApiClients } from '@/api/context'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { manifestKeys } from '@/lib/query-keys'
import type { ManifestVersion } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

const APPLY_STATUS_LABEL: Record<number, string> = {
  [ApplyStatus.APPLIED]: 'APPLIED',
  [ApplyStatus.FAILED]: 'FAILED',
  [ApplyStatus.ROLLED_BACK]: 'ROLLED_BACK',
}

const columns: ColumnDef<ManifestVersion>[] = [
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

export function ManifestHistoryTable() {
  const { manifestHistory } = useApiClients()
  const [selectedVersion, setSelectedVersion] = useState<ManifestVersion | null>(null)

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

  return (
    <>
      <DataTable<ManifestVersion>
        queryKey={manifestKeys.history()}
        queryFn={fetchVersions}
        columns={columns}
        pageSize={20}
        onRowClick={setSelectedVersion}
      />

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
    </>
  )
}
