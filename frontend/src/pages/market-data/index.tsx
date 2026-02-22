import type { ColumnDef } from '@tanstack/react-table'
import { useNavigate } from 'react-router-dom'
import { DataTable } from '@/components/shared/data-table'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'

interface DataSetRow {
  id: string
  code: string
  displayName: string
  category: number
  unit: string
  status: number
  createdAt?: { seconds: bigint | number; nanos?: number } | null
}

function dataSetStatusLabel(status: number): string {
  switch (status) {
    case 1:
      return 'DRAFT'
    case 2:
      return 'ACTIVE'
    case 3:
      return 'DEPRECATED'
    default:
      return 'UNKNOWN'
  }
}

function dataCategoryLabel(category: number): string {
  switch (category) {
    case 1:
      return 'FX Rate'
    case 2:
      return 'Interest Rate'
    case 3:
      return 'Commodity Price'
    case 4:
      return 'Equity Price'
    case 5:
      return 'Index Value'
    case 6:
      return 'Energy Price'
    case 7:
      return 'Carbon Price'
    case 8:
      return 'Benchmark Rate'
    case 9:
      return 'Volatility'
    case 10:
      return 'Credit Spread'
    default:
      return 'Unknown'
  }
}

const STATUS_OPTIONS = [
  { label: 'Draft', value: '1' },
  { label: 'Active', value: '2' },
  { label: 'Deprecated', value: '3' },
]

const CATEGORY_OPTIONS = [
  { label: 'FX Rate', value: '1' },
  { label: 'Interest Rate', value: '2' },
  { label: 'Commodity Price', value: '3' },
  { label: 'Equity Price', value: '4' },
  { label: 'Index Value', value: '5' },
  { label: 'Energy Price', value: '6' },
  { label: 'Carbon Price', value: '7' },
  { label: 'Benchmark Rate', value: '8' },
  { label: 'Volatility', value: '9' },
  { label: 'Credit Spread', value: '10' },
]

const columns: ColumnDef<DataSetRow>[] = [
  {
    accessorKey: 'code',
    header: 'Code',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.code}</span>
    ),
  },
  {
    accessorKey: 'displayName',
    header: 'Name',
    cell: ({ row }) => (
      <span>{row.original.displayName || row.original.code}</span>
    ),
  },
  {
    accessorKey: 'category',
    header: 'Category',
    cell: ({ row }) => (
      <span className="text-sm text-muted-foreground">
        {dataCategoryLabel(row.original.category)}
      </span>
    ),
  },
  {
    accessorKey: 'unit',
    header: 'Unit',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.unit}</span>
    ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => (
      <StatusBadge status={dataSetStatusLabel(row.original.status)} />
    ),
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} format="relative" />,
  },
]

export function MarketDataPage() {
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()
  const navigate = useNavigate()

  if (!tenantSlug) {
    return (
      <div className="p-6">
        <p className="text-muted-foreground">No tenant selected.</p>
      </div>
    )
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Market Data</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Market data sets with price observations for FX rates, interest rates, energy prices, and more.
        </p>
      </div>

      <DataTable<DataSetRow>
        queryKey={[...tenantKeys.all(tenantSlug), 'market-data', 'datasets']}
        queryFn={async ({ pageToken, pageSize, filters }) => {
          const statusFilter = filters?.status ? parseInt(filters.status, 10) : 0
          const categoryFilter = filters?.category ? parseInt(filters.category, 10) : 0
          const res = await clients.marketInformation.listDataSets({
            statusFilter,
            categoryFilter,
            pageSize,
            pageToken: pageToken ?? '',
          })
          return {
            items: res.datasets.map((d) => ({
              id: d.id,
              code: d.code,
              displayName: d.displayName,
              category: d.category,
              unit: d.unit,
              status: d.status,
              createdAt: d.createdAt ?? null,
            })),
            nextPageToken: res.nextPageToken || undefined,
          }
        }}
        columns={columns}
        pageSize={25}
        filters={[
          {
            field: 'category',
            label: 'Category',
            type: 'select',
            options: CATEGORY_OPTIONS,
          },
          {
            field: 'status',
            label: 'Status',
            type: 'select',
            options: STATUS_OPTIONS,
          },
        ]}
        onRowClick={(row) => navigate(`/market-data/${row.code}`)}
      />
    </div>
  )
}
