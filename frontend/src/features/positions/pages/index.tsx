import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { TimeDisplay } from '@/shared/time-display'
import { Card } from '@/components/ui/card'
import { PageHeader } from '@/shared/page-header'
import { PageShell } from '@/shared/page-shell'
import { TransactionStatus } from '@/api/gen/meridian/common/v1/types_pb'
import { usePositionLogsTable } from '../hooks'

const TRANSACTION_STATUS_NAMES: Record<number, string> = {
  [TransactionStatus.PENDING]: 'PENDING',
  [TransactionStatus.POSTED]: 'POSTED',
  [TransactionStatus.FAILED]: 'FAILED',
  [TransactionStatus.CANCELLED]: 'CANCELLED',
  [TransactionStatus.REVERSED]: 'REVERSED',
}

export interface PositionEntry {
  entryId: string
  transactionId: string
  accountId: string
  amount: {
    amount: bigint | string
    currency: string
  }
  direction: string
  qualityLevel: string
  transactionDate?: { seconds: bigint | number; nanos?: number }
  description?: string
  reference?: string
}

export interface FinancialPositionLog {
  logId: string
  accountId: string
  accountServiceDomain?: number
  statusTracking?: {
    currentStatus: string
  }
  createdAt?: { seconds: bigint | number; nanos?: number }
  updatedAt?: { seconds: bigint | number; nanos?: number }
  transactionLogEntries?: TransactionLogEntry[]
}

export interface TransactionLogEntry {
  entryId: string
  transactionId: string
  accountId: string
  amount?: {
    amount: bigint | string
    currency: string
  }
  direction: string
  qualityLevel?: string
  timestamp?: { seconds: bigint | number; nanos?: number }
  description?: string
  reference?: string
}

export function PositionsPage() {
  const navigate = useNavigate()
  const { queryKey, queryFn } = usePositionLogsTable()

  const columns: ColumnDef<FinancialPositionLog>[] = [
    {
      accessorKey: 'logId',
      header: 'Log ID',
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.original.logId.slice(0, 8)}…
        </span>
      ),
    },
    {
      accessorKey: 'accountId',
      header: 'Account',
      cell: ({ row }) => (
        <span className="font-mono text-xs">{row.original.accountId}</span>
      ),
    },
    {
      accessorKey: 'statusTracking',
      header: 'Status',
      cell: ({ row }) => {
        const status = row.original.statusTracking?.currentStatus
        const label = typeof status === 'number' ? TRANSACTION_STATUS_NAMES[status] : (typeof status === 'string' ? status.replace(/_/g, ' ') : null)
        return <span className="text-sm">{label ?? '—'}</span>
      },
    },
    {
      accessorKey: 'createdAt',
      header: 'Created',
      cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} />,
    },
    {
      accessorKey: 'updatedAt',
      header: 'Last Updated',
      cell: ({ row }) => <TimeDisplay timestamp={row.original.updatedAt} />,
    },
  ]

  const filters = [
    {
      field: 'accountId',
      label: 'Account ID',
      type: 'text' as const,
    },
    {
      field: 'status',
      label: 'Status',
      type: 'select' as const,
      options: [
        { label: 'Pending', value: String(TransactionStatus.PENDING) },
        { label: 'Posted', value: String(TransactionStatus.POSTED) },
        { label: 'Failed', value: String(TransactionStatus.FAILED) },
        { label: 'Cancelled', value: String(TransactionStatus.CANCELLED) },
        { label: 'Reversed', value: String(TransactionStatus.REVERSED) },
      ],
    },
  ]

  const handleRowClick = (log: FinancialPositionLog) => {
    navigate(`/positions/${log.logId}`)
  }

  return (
    <PageShell>
      <PageHeader
        title="Positions"
        description="Financial position logs with bi-temporal data quality tracking."
      />

      <Card>
        <DataTable
          queryKey={queryKey}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          filters={filters}
          onRowClick={handleRowClick}
        />
      </Card>
    </PageShell>
  )
}

