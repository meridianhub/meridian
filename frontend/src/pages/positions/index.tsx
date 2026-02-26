import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/components/shared/data-table'
import { TimeDisplay } from '@/components/shared/time-display'
import { useApiClients } from '@/api/context'
import { Card } from '@/components/ui/card'
import { TransactionStatus } from '@/api/gen/meridian/common/v1/types_pb'

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

interface ListPositionLogsParams {
  pageToken?: string
  pageSize: number
  filters?: Record<string, string>
}

interface ListPositionLogsResult {
  items: FinancialPositionLog[]
  nextPageToken?: string
}

export function PositionsPage() {
  const navigate = useNavigate()
  const clients = useApiClients()

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
        return <span className="text-sm">{typeof status === 'string' ? status.replace(/_/g, ' ') : '—'}</span>
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

  const queryFn = async (params: ListPositionLogsParams): Promise<ListPositionLogsResult> => {
    const statusValue = params.filters?.status
    const response = await clients.positionKeeping.listFinancialPositionLogs({
      pageToken: params.pageToken ?? '',
      accountId: params.filters?.accountId ?? '',
      status: statusValue ? (Number(statusValue) as TransactionStatus) : TransactionStatus.UNSPECIFIED,
      pagination: {
        pageSize: params.pageSize,
        pageToken: params.pageToken ?? '',
      },
    })

    return {
      items: (response.logs ?? []) as FinancialPositionLog[],
      nextPageToken: response.pagination?.nextPageToken,
    }
  }

  const handleRowClick = (log: FinancialPositionLog) => {
    navigate(`/positions/${log.logId}`)
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Positions</h1>
        <p className="mt-2 text-muted-foreground">
          Financial position logs with bi-temporal data quality tracking.
        </p>
      </div>

      <Card className="p-6">
        <DataTable
          queryKey={['positions']}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          filters={filters}
          onRowClick={handleRowClick}
        />
      </Card>
    </div>
  )
}

