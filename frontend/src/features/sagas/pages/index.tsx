import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { DataTable } from '@/shared/data-table'
import { PageShell, PageHeader, Breadcrumbs } from '@/shared'
import type { SagaDefinition } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { useSagasTable } from '../hooks'
import { usePageTitle } from '@/hooks/use-page-title'
import { CreateSagaDraftDialog } from './create-saga-draft-dialog'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function triggerType(trigger: string): string {
  const idx = trigger.indexOf(':')
  return idx >= 0 ? trigger.slice(0, idx) : trigger
}

// ---------------------------------------------------------------------------
// Page Component
// ---------------------------------------------------------------------------

export function StarlarkConfigPage() {
  usePageTitle('Starlark Config')
  const { queryKey, queryFn } = useSagasTable()
  const [createDialogOpen, setCreateDialogOpen] = useState(false)

  const columns = useMemo((): ColumnDef<SagaDefinition>[] => [
    {
      accessorKey: 'name',
      header: 'Name',
      cell: (row) => (
        <Link
          to={`/starlark-config/${row.row.original.name}`}
          className="font-mono text-sm text-primary hover:underline"
        >
          {row.row.original.name}
        </Link>
      ),
      size: 220,
    },
    {
      accessorKey: 'trigger',
      header: 'Trigger',
      cell: (row) => {
        const type = triggerType(row.row.original.trigger)
        return (
          <div className="flex items-center gap-2">
            <Badge variant={type === 'event' ? 'secondary' : 'outline'}>{type}</Badge>
            <span className="font-mono text-xs text-muted-foreground truncate max-w-[300px]">
              {row.row.original.trigger}
            </span>
          </div>
        )
      },
      size: 400,
    },
  ], [])

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
