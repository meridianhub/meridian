import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import type { FilterConfig } from '@/shared/data-table'
import { MoneyDisplay } from '@/shared/money-display'
import { StatusBadge } from '@/shared/status-badge'
import { PageHeader } from '@/shared/page-header'
import { PageShell } from '@/shared/page-shell'
import { Card } from '@/components/ui/card'
import { usePageTitle } from '@/hooks/use-page-title'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { ConnectError, Code } from '@connectrpc/connect'
import { useInvoicesTable } from '../api/hooks'
import type { Invoice } from '../api/types'

const STATUS_OPTIONS = [
  { label: 'Draft', value: 'DRAFT' },
  { label: 'Issued', value: 'ISSUED' },
  { label: 'Paid', value: 'PAID' },
  { label: 'Void', value: 'VOID' },
  { label: 'Overdue', value: 'OVERDUE' },
]

function useBillingRunOptions() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const { data: runs = [] } = useQuery({
    queryKey: [...tenantKeys.billingRuns(tenantSlug ?? ''), 'filter-options'],
    queryFn: async () => {
      if (!tenantSlug) return []
      try {
        const allRuns = []
        let pageToken = ''
        do {
          const response = await clients.billing.listBillingRuns({
            pagination: { pageSize: 100, pageToken },
          })
          allRuns.push(...(response.billingRuns ?? []))
          pageToken = response.pagination?.nextPageToken ?? ''
        } while (pageToken)
        return allRuns
      } catch (error) {
        if (
          error instanceof ConnectError &&
          (error.code === Code.NotFound || error.code === Code.Unimplemented)
        ) {
          return []
        }
        throw error
      }
    },
    enabled: Boolean(tenantSlug),
  })

  return runs.map((run) => ({
    label: run.id ?? '',
    value: run.id ?? '',
  }))
}

const columns: ColumnDef<Invoice>[] = [
  {
    accessorKey: 'invoiceNumber',
    header: 'Invoice #',
  },
  {
    accessorKey: 'partyId',
    header: 'Party',
  },
  {
    accessorKey: 'subtotalCents',
    header: 'Amount',
    cell: ({ row }) => (
      <MoneyDisplay amount={String(row.original.subtotalCents)} currency={row.original.currency} />
    ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'dueDate',
    header: 'Due Date',
    cell: ({ row }) => {
      const dueDate = row.original.dueDate
      if (!dueDate) return <span className="text-muted-foreground">-</span>
      // Parse YYYY-MM-DD as local midnight to avoid UTC-offset day shifts
      const parts = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dueDate)
      const d = parts
        ? new Date(Number(parts[1]), Number(parts[2]) - 1, Number(parts[3]))
        : new Date(dueDate)
      return isNaN(d.getTime()) ? (
        <span>{dueDate}</span>
      ) : (
        <span>{d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })}</span>
      )
    },
  },
]

export function InvoicesPage() {
  usePageTitle('Invoices')
  const navigate = useNavigate()
  const { queryKey, queryFn } = useInvoicesTable()
  const billingRunOptions = useBillingRunOptions()

  const filterConfigs: FilterConfig[] = [
    {
      field: 'status',
      label: 'Status',
      type: 'select',
      options: STATUS_OPTIONS,
    },
    {
      field: 'party_id',
      label: 'Party',
      type: 'text',
    },
    {
      field: 'billing_run_id',
      label: 'Billing Run',
      type: 'select',
      options: billingRunOptions,
    },
  ]

  function handleRowClick(row: Invoice) {
    void navigate(`/billing/invoices/${row.id}`)
  }

  return (
    <PageShell>
      <PageHeader title="Invoices" />
      <Card>
        <DataTable
          queryKey={queryKey}
          queryFn={queryFn}
          columns={columns}
          filters={filterConfigs}
          onRowClick={handleRowClick}
          emptyState={
            <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
              <span className="text-sm font-medium">No invoices yet</span>
              <span className="text-xs">Invoices will appear here once billing runs are processed.</span>
            </div>
          }
        />
      </Card>
    </PageShell>
  )
}
