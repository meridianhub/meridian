import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/components/shared/data-table'
import { StatusBadge } from '@/components/shared/status-badge'
import { Badge } from '@/components/ui/badge'

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

async function fetchReconciliationRuns(params: {
  pageToken?: string
  pageSize: number
  filters?: Record<string, string>
}): Promise<{ items: ReconciliationRun[]; nextPageToken?: string }> {
  const url = new URL('/api/v1/reconciliation/runs', window.location.origin)
  url.searchParams.set('page_size', String(params.pageSize))
  if (params.pageToken) url.searchParams.set('page_token', params.pageToken)
  if (params.filters) {
    for (const [k, v] of Object.entries(params.filters)) {
      if (v) url.searchParams.set(k, v)
    }
  }
  const res = await fetch(url.toString())
  if (!res.ok) throw new Error(`Failed to fetch reconciliation runs: ${res.status}`)
  const data = await res.json() as {
    runs?: Array<{
      runId?: string
      accountId?: string
      scope?: string
      settlementType?: string
      status?: string
      varianceCount?: number
      periodStart?: string
      periodEnd?: string
    }>
    nextPageToken?: string
  }
  return {
    items: (data.runs ?? []).map((run) => ({
      runId: run.runId ?? '',
      accountId: run.accountId ?? '',
      scope: run.scope?.replace('RECONCILIATION_SCOPE_', '') ?? '',
      settlementType: run.settlementType?.replace('SETTLEMENT_TYPE_', '') ?? '',
      status: run.status?.replace('RUN_STATUS_', '') ?? '',
      varianceCount: run.varianceCount ?? 0,
      periodStart: run.periodStart ?? '',
      periodEnd: run.periodEnd ?? '',
    })),
    nextPageToken: data.nextPageToken,
  }
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

  function handleRowClick(run: ReconciliationRun) {
    void navigate(`/reconciliation/${run.runId}`)
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Reconciliation</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Settlement runs, variance detection, and dispute resolution.
        </p>
      </div>
      <DataTable
        queryKey={['reconciliation-runs']}
        queryFn={fetchReconciliationRuns}
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
