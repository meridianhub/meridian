import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { DataTable } from '@/shared/data-table'
import { TimeDisplay } from '@/shared/time-display'
import { StatusBadge } from '@/shared/status-badge'
import { PageShell, PageHeader, Breadcrumbs } from '@/shared'
import type { SagaDefinition } from '@/api/gen/meridian/saga/v1/saga_registry_pb'
import { SagaStatus } from '@/api/gen/meridian/saga/v1/saga_registry_pb'
import { useSagasTable } from '../hooks'
import { usePageTitle } from '@/hooks/use-page-title'
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
    <span className="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium bg-info-muted text-info-foreground border-info/30">
      Platform Default
    </span>
  ) : (
    <span className="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium bg-secondary text-secondary-foreground border-border">
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
  usePageTitle('Starlark Config')
  const { queryKey, queryFn } = useSagasTable()
  const [createDialogOpen, setCreateDialogOpen] = useState(false)

  const columns = useMemo((): ColumnDef<SagaDefinition>[] => {
    const base: ColumnDef<SagaDefinition>[] = [
      {
        accessorKey: 'name',
        header: 'Name',
        cell: (row) => (
          <Link
            to={`/starlark-config/${row.row.original.id}`}
            className="font-mono text-sm text-primary hover:underline"
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

  return (
    <PageShell>
      <Breadcrumbs items={[
        { label: 'Economy', href: '/economy' },
        { label: 'Starlark Config' },
      ]} />

      <PageHeader
        title="Starlark Configuration"
        description="Manage saga workflow definitions and Starlark scripts"
        actions={<Button onClick={() => setCreateDialogOpen(true)}>Create Saga</Button>}
      />

      <CreateSagaDraftDialog open={createDialogOpen} onOpenChange={setCreateDialogOpen} />

      <Card className="p-6">
        <DataTable<SagaDefinition>
          queryKey={queryKey}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          className="w-full"
        />
      </Card>
    </PageShell>
  )
}
