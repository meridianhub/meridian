import * as React from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { useNavigate } from 'react-router-dom'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { PageShell } from '@/shared/page-shell'
import { PageHeader } from '@/shared/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { useDatasetsTable } from '../hooks'
import { CATEGORY_OPTIONS, STATUS_OPTIONS } from './constants'
import { RegisterDataSetDialog } from './register-dataset-dialog'

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
  const { queryKey, queryFn, tenantSlug } = useDatasetsTable()
  const navigate = useNavigate()
  const [registerOpen, setRegisterOpen] = React.useState(false)

  if (!tenantSlug) {
    return (
      <div className="p-6">
        <p className="text-muted-foreground">No tenant selected.</p>
      </div>
    )
  }

  return (
    <PageShell>
      <PageHeader
        title="Market Data"
        description="Market data sets with price observations for FX rates, interest rates, energy prices, and more."
        actions={<Button onClick={() => setRegisterOpen(true)}>Register Dataset</Button>}
      />

      <Card className="p-6">
        <DataTable<DataSetRow>
          queryKey={queryKey}
          queryFn={queryFn}
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
      </Card>

      <RegisterDataSetDialog open={registerOpen} onOpenChange={setRegisterOpen} />
    </PageShell>
  )
}
