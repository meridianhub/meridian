import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useReconciliationRunsTable } from '../hooks'
import { InitiateReconciliationDialog } from './initiate-reconciliation-dialog'

export interface ReconciliationRun {
  runId: string
  accountId: string
  scope: string
  settlementType: string
  status: string
  varianceCount: number
  periodStart: string
  periodEnd: string
}

function formatDate(iso: string): string {
  if (!iso) return '—'
  return iso.slice(0, 10)
}

const columns: ColumnDef<ReconciliationRun>[] = [
  {
    accessorKey: 'runId',
    header: 'Run ID',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.runId}</span>
    ),
  },
  {
    accessorKey: 'accountId',
    header: 'Account',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.accountId}</span>
    ),
  },
  {
    accessorKey: 'scope',
    header: 'Scope',
  },
  {
    accessorKey: 'settlementType',
    header: 'Settlement Type',
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'varianceCount',
    header: 'Variances',
    cell: ({ row }) => {
      const count = row.original.varianceCount
      return (
        <Badge variant={count > 0 ? 'destructive' : 'secondary'}>
          {count}
        </Badge>
      )
    },
  },
  {
    id: 'period',
    header: 'Period',
    cell: ({ row }) => {
      const { periodStart, periodEnd } = row.original
      return (
        <span className="text-sm text-muted-foreground">
          {formatDate(periodStart)} – {formatDate(periodEnd)}
        </span>
      )
    },
  },
]

export function ReconciliationPage() {
  const navigate = useNavigate()
  const { queryKey, queryFn } = useReconciliationRunsTable()
  const [dialogOpen, setDialogOpen] = React.useState(false)

  function handleRowClick(run: ReconciliationRun) {
    void navigate(`/reconciliation/${run.runId}`)
  }

  function handleReconciliationSuccess(runId: string) {
    void navigate(`/reconciliation/${runId}`)
  }

  return (
    <div className="p-6">
      <div className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Reconciliation</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Settlement runs, variance detection, and dispute resolution.
          </p>
        </div>
        <Button onClick={() => setDialogOpen(true)}>Start Reconciliation</Button>
      </div>
      <InitiateReconciliationDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onSuccess={handleReconciliationSuccess}
      />
      <DataTable
        queryKey={queryKey}
        queryFn={queryFn}
        columns={columns}
        pageSize={25}
        filters={[
          {
            field: 'status',
            label: 'Status',
            type: 'select',
            options: [
              { label: 'Running', value: 'RUN_STATUS_RUNNING' },
              { label: 'Completed', value: 'RUN_STATUS_COMPLETED' },
              { label: 'Failed', value: 'RUN_STATUS_FAILED' },
            ],
          },
          { field: 'account_id', label: 'Account ID', type: 'text' },
        ]}
        onRowClick={handleRowClick}
      />
    </div>
  )
}
