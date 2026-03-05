import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { MoneyDisplay } from '@/shared/money-display'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { usePaymentsTable } from '../hooks'
import type { PaymentOrder } from './payments-query'

const STATUS_OPTIONS = [
  { label: 'Initiated', value: 'INITIATED' },
  { label: 'Reserved', value: 'RESERVED' },
  { label: 'Executing', value: 'EXECUTING' },
  { label: 'Completed', value: 'COMPLETED' },
  { label: 'Failed', value: 'FAILED' },
  { label: 'Cancelled', value: 'CANCELLED' },
  { label: 'Reversed', value: 'REVERSED' },
]

const FILTER_CONFIGS = [
  {
    field: 'status',
    label: 'Status',
    type: 'select' as const,
    options: STATUS_OPTIONS,
  },
]

const columns: ColumnDef<PaymentOrder>[] = [
  {
    accessorKey: 'paymentOrderId',
    header: 'Payment ID',
  },
  {
    accessorKey: 'debtorAccountId',
    header: 'Debtor Account',
  },
  {
    accessorKey: 'creditorReference',
    header: 'Creditor Reference',
  },
  {
    accessorKey: 'amount',
    header: 'Amount',
    cell: ({ row }) => (
      <MoneyDisplay amount={row.original.amount} currency={row.original.currency} />
    ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} format="relative" />,
  },
]

interface PaymentsPageProps {
  onRowNavigate?: (paymentOrderId: string) => void
}

export function PaymentsPage({ onRowNavigate }: PaymentsPageProps = {}) {
  const navigate = useNavigate()
  const { queryKey, queryFn } = usePaymentsTable()

  function handleRowClick(row: PaymentOrder) {
    if (onRowNavigate) {
      onRowNavigate(row.paymentOrderId)
    } else {
      void navigate(`/payments/${row.paymentOrderId}`)
    }
  }

  return (
    <div className="p-6">
      <h1 className="mb-6 text-2xl font-semibold">Payments</h1>
      <DataTable
        queryKey={queryKey}
        queryFn={queryFn}
        columns={columns}
        filters={FILTER_CONFIGS}
        onRowClick={handleRowClick}
        emptyState={
          <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
            <span className="text-sm font-medium">No payments yet</span>
            <span className="text-xs">Payments will appear here once initiated.</span>
          </div>
        }
      />
    </div>
  )
}
