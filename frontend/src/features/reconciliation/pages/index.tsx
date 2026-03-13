import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { PageHeader } from '@/shared/page-header'
import { PageShell } from '@/shared/page-shell'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { useReconciliationRunsTable } from '../hooks'
import { usePageTitle } from '@/hooks/use-page-title'
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
  usePageTitle('Reconciliation')
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
    <PageShell>
      <PageHeader
        title="Reconciliation"
        description="Settlement runs, variance detection, and dispute resolution."
        actions={<Button onClick={() => setDialogOpen(true)}>Start Reconciliation</Button>}
      />
      <InitiateReconciliationDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onSuccess={handleReconciliationSuccess}
      />
      <Card>
        <DataTable
          queryKey={queryKey}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          emptyState={
            <div data-testid="empty-state" className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
              <span className="text-sm font-medium">No reconciliation runs yet</span>
              <span className="text-xs">Start a reconciliation to see results here.</span>
            </div>
          }
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
      </Card>
    </PageShell>
  )
}
