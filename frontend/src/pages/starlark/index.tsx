import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { DataTable, type DataTableQueryParams, type DataTableResult } from '@/components/shared/data-table'
import { TimeDisplay } from '@/components/shared/time-display'
import { StatusBadge } from '@/components/shared/status-badge'
import { useApiClients } from '@/api/context'
import type { SagaDefinition } from '@/api/gen/meridian/saga/v1/saga_registry_pb'
import { SagaStatus } from '@/api/gen/meridian/saga/v1/saga_registry_pb'
import { CreateSagaDraftDialog } from './create-saga-draft-dialog'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function sagaStatusLabel(status: SagaStatus): string {
  switch (status) {
    case SagaStatus.ACTIVE:
      return 'ACTIVE'
    case SagaStatus.DRAFT:
      return 'DRAFT'
    case SagaStatus.DEPRECATED:
      return 'DEPRECATED'
    default:
      return 'UNKNOWN'
  }
}

function SourceBadge({ isSystem }: { isSystem: boolean }) {
  return isSystem ? (
    <span className="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium bg-blue-50 text-blue-700 border-blue-200">
      Platform Default
    </span>
  ) : (
    <span className="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium bg-purple-50 text-purple-700 border-purple-200">
      Tenant Override
    </span>
  )
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface StarlarkConfigPageProps {
  isPlatformAdmin?: boolean
}

// ---------------------------------------------------------------------------
// Page Component
// ---------------------------------------------------------------------------

export function StarlarkConfigPage({ isPlatformAdmin = false }: StarlarkConfigPageProps) {
  const { sagaRegistry } = useApiClients()
  const [createDialogOpen, setCreateDialogOpen] = useState(false)

  const columns = useMemo((): ColumnDef<SagaDefinition>[] => {
    const base: ColumnDef<SagaDefinition>[] = [
      {
        accessorKey: 'name',
        header: 'Name',
        cell: (row) => (
          <Link
            to={`/starlark-config/${row.row.original.id}`}
            className="font-mono text-sm text-blue-600 hover:underline"
          >
            {row.row.original.name}
          </Link>
        ),
        size: 220,
      },
      {
        accessorKey: 'version',
        header: 'Version',
        cell: (row) => <span className="font-mono text-sm">v{row.row.original.version}</span>,
        size: 80,
      },
      {
        accessorKey: 'status',
        header: 'Status',
        cell: (row) => <StatusBadge status={sagaStatusLabel(row.row.original.status)} />,
        size: 110,
      },
      {
        accessorKey: 'displayName',
        header: 'Display Name',
        cell: (row) => <span className="text-sm">{row.row.original.displayName || '—'}</span>,
        size: 200,
      },
      {
        accessorKey: 'updatedAt',
        header: 'Updated',
        cell: (row) => (
          <TimeDisplay timestamp={row.row.original.updatedAt} format="relative" />
        ),
        size: 150,
      },
    ]

    if (isPlatformAdmin) {
      base.push({
        id: 'overrides',
        header: 'Overrides',
        cell: () => <span className="text-sm text-muted-foreground">—</span>,
        size: 100,
      })
    } else {
      base.push({
        id: 'source',
        header: 'Source',
        cell: (row) => <SourceBadge isSystem={row.row.original.isSystem} />,
        size: 140,
      })
    }

    return base
  }, [isPlatformAdmin])

  async function fetchSagas(params: DataTableQueryParams): Promise<DataTableResult<SagaDefinition>> {
    const response = await sagaRegistry.listSagas({
      pageSize: params.pageSize,
      pageToken: params.pageToken,
    })
    return {
      items: response.sagas ?? [],
      nextPageToken: response.nextPageToken || undefined,
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Starlark Configuration</h1>
          <p className="mt-2 text-muted-foreground">
            Manage saga workflow definitions and Starlark scripts
          </p>
        </div>
        <Button onClick={() => setCreateDialogOpen(true)}>Create Saga</Button>
      </div>

      <CreateSagaDraftDialog open={createDialogOpen} onOpenChange={setCreateDialogOpen} />

      <Card className="p-6">
        <DataTable<SagaDefinition>
          queryKey={['starlark-config']}
          queryFn={fetchSagas}
          columns={columns}
          pageSize={25}
          className="w-full"
        />
      </Card>
    </div>
  )
}
