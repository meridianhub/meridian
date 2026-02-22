import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { DataTable, type DataTableQueryParams, type DataTableResult } from '@/components/shared/data-table'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared'
import type { FinancialBookingLog } from './types'

function getStatusName(status: unknown): string {
  if (typeof status === 'string') return status
  if (typeof status === 'number') {
    const statusMap: Record<number, string> = {
      0: 'UNSPECIFIED',
      1: 'PENDING',
      2: 'POSTED',
      3: 'FAILED',
      4: 'CANCELLED',
      5: 'REVERSED',
    }
    return statusMap[status] ?? String(status)
  }
  return String(status ?? '')
}

function getCurrencyName(currency: unknown): string {
  if (typeof currency === 'string') return currency
  if (typeof currency === 'number') {
    const currencyMap: Record<number, string> = {
      0: 'UNSPECIFIED',
      1: 'GBP',
      2: 'USD',
      3: 'EUR',
      4: 'JPY',
      5: 'AUD',
      6: 'CAD',
      7: 'CHF',
      8: 'CNY',
      9: 'INR',
      10: 'SGD',
      11: 'HKD',
    }
    return currencyMap[currency] ?? String(currency)
  }
  return String(currency ?? '')
}

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
    accessorKey: 'baseCurrency',
    header: 'Currency',
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    id: 'postingCount',
    header: 'Postings',
    cell: ({ row }) => row.original.postings?.length ?? 0,
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
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()
  const navigate = useNavigate()

  async function fetchBookingLogs(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<FinancialBookingLog>> {
    if (!tenantSlug) return { items: [] }

    const statusFilter = params.filters?.status

    const response = await clients.financialAccounting.listFinancialBookingLogs({
      pagination: { pageSize: params.pageSize, pageToken: params.pageToken ?? '' },
      ...(statusFilter !== undefined && { status: statusFilter as never }),
    })

    const items = (response.financialBookingLogs ?? []).map((log) => ({
      id: log.id,
      financialAccountType: String(log.financialAccountType ?? ''),
      productServiceReference: String(log.productServiceReference ?? ''),
      businessUnitReference: String(log.businessUnitReference ?? ''),
      chartOfAccountsRules: String(log.chartOfAccountsRules ?? ''),
      baseCurrency: getCurrencyName(log.baseCurrency),
      status: getStatusName(log.status),
      createdAt: log.createdAt ?? null,
      updatedAt: log.updatedAt ?? null,
      postings: (log.postings ?? []) as FinancialBookingLog['postings'],
    })) as FinancialBookingLog[]

    const nextPageToken =
      typeof response.pagination?.nextPageToken === 'string'
        ? response.pagination.nextPageToken
        : undefined

    return {
      items,
      nextPageToken: nextPageToken || undefined,
    }
  }

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
        queryKey={[...(tenantSlug ? tenantKeys.all(tenantSlug) : ['no-tenant']), 'ledger', 'bookingLogs']}
        queryFn={fetchBookingLogs}
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
