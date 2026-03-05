import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared'
import { useBookingLogsTable } from '../hooks'
import type { FinancialBookingLog } from './types'

const columns: ColumnDef<FinancialBookingLog>[] = [
  {
    accessorKey: 'id',
    header: 'Log ID',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.id}</span>
    ),
  },
  {
    accessorKey: 'financialAccountType',
    header: 'Account Type',
  },
  {
    accessorKey: 'businessUnitReference',
    header: 'Business Unit',
  },
  {
    accessorKey: 'instrumentCode',
    header: 'Instrument',
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => (
      <TimeDisplay timestamp={row.original.createdAt} format="relative" />
    ),
  },
]

export function LedgerPage() {
  const { queryKey, queryFn } = useBookingLogsTable()
  const navigate = useNavigate()

  function handleRowClick(row: FinancialBookingLog) {
    void navigate(`/ledger/${row.id}`)
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Ledger</h1>
        <p className="text-muted-foreground">
          Financial booking logs and double-entry postings
        </p>
      </div>

      <DataTable<FinancialBookingLog>
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
              { label: 'Pending', value: 'PENDING' },
              { label: 'Posted', value: 'POSTED' },
              { label: 'Failed', value: 'FAILED' },
              { label: 'Cancelled', value: 'CANCELLED' },
              { label: 'Reversed', value: 'REVERSED' },
            ],
          },
        ]}
        onRowClick={handleRowClick}
      />
    </div>
  )
}
