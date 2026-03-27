import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { MoneyDisplay } from '@/shared/money-display'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { PageHeader } from '@/shared/page-header'
import { PageShell } from '@/shared/page-shell'
import { Card } from '@/components/ui/card'
import { usePageTitle } from '@/hooks/use-page-title'
import { useBillingRunsTable } from '../api/hooks'
import type { BillingRun } from '../api/types'

function isoToDate(iso: string): string {
  if (!iso) return '—'
  return iso.slice(0, 10)
}

function isoToTimestamp(iso: string): { seconds: number; nanos: number } | null {
  if (!iso) return null
  const ms = Date.parse(iso)
  if (isNaN(ms)) return null
  return { seconds: Math.floor(ms / 1000), nanos: (ms % 1000) * 1_000_000 }
}

const STATUS_OPTIONS = [
  { label: 'Initiated', value: 'INITIATED' },
  { label: 'Processing', value: 'PROCESSING' },
  { label: 'Completed', value: 'COMPLETED' },
  { label: 'Failed', value: 'FAILED' },
]

const FILTER_CONFIGS = [
  {
    field: 'status',
    label: 'Status',
    type: 'select' as const,
    options: STATUS_OPTIONS,
  },
]

const columns: ColumnDef<BillingRun>[] = [
  {
    accessorKey: 'billingPeriod',
    header: 'Period',
    cell: ({ row }) => {
      const { start, end } = row.original.billingPeriod
      return (
        <span className="tabular-nums text-sm">
          {isoToDate(start)} – {isoToDate(end)}
        </span>
      )
    },
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'invoiceCount',
    header: 'Invoices',
  },
  {
    accessorKey: 'totalAmountCents',
    header: 'Total Amount',
    cell: ({ row }) => (
      <MoneyDisplay
        amount={BigInt(Math.round(row.original.totalAmountCents))}
        currency={row.original.currency}
      />
    ),
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => (
      <TimeDisplay timestamp={isoToTimestamp(row.original.createdAt)} format="relative" />
    ),
  },
]

interface BillingRunsPageProps {
  onRowNavigate?: (id: string) => void
}

export function BillingRunsPage({ onRowNavigate }: BillingRunsPageProps = {}) {
  usePageTitle('Billing Runs')
  const navigate = useNavigate()
  const { queryKey, queryFn } = useBillingRunsTable()

  function handleRowClick(row: BillingRun) {
    if (onRowNavigate) {
      onRowNavigate(row.id)
    } else {
      void navigate(`/billing/runs/${row.id}`)
    }
  }

  return (
    <PageShell>
      <PageHeader title="Billing Runs" description="Billing run history and status" />
      <Card>
        <DataTable
          queryKey={queryKey}
          queryFn={queryFn}
          columns={columns}
          filters={FILTER_CONFIGS}
          onRowClick={handleRowClick}
          emptyState={
            <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
              <span className="text-sm font-medium">No billing runs yet</span>
              <span className="text-xs">Billing runs will appear here once initiated.</span>
            </div>
          }
        />
      </Card>
    </PageShell>
  )
}
