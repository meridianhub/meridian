import { useQuery } from '@tanstack/react-query'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
} from '@tanstack/react-table'
import { Card } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Breadcrumbs } from '@/shared/breadcrumbs'
import { PageShell } from '@/shared/page-shell'
import { PageHeader } from '@/shared/page-header'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import { usePageTitle } from '@/hooks/use-page-title'
import {
  ValuationMethod,
  type ValuationRule,
} from '@/api/gen/meridian/control_plane/v1/manifest_pb'

function methodLabel(method: ValuationMethod): string {
  switch (method) {
    case ValuationMethod.SPOT_RATE:
      return 'Spot Rate'
    case ValuationMethod.FIXED:
      return 'Fixed'
    default:
      return 'Unknown'
  }
}

const columns: ColumnDef<ValuationRule>[] = [
  {
    id: 'ruleName',
    header: 'Rule Name',
    cell: ({ row }) => (
      <span className="font-mono text-sm font-medium">
        {row.original.fromInstrument} → {row.original.toInstrument}
      </span>
    ),
  },
  {
    accessorKey: 'fromInstrument',
    header: 'From Instrument',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.fromInstrument}</span>
    ),
  },
  {
    accessorKey: 'toInstrument',
    header: 'To Instrument',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.toInstrument}</span>
    ),
  },
  {
    accessorKey: 'method',
    header: 'Method',
    cell: ({ row }) => (
      <span className="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium bg-secondary text-secondary-foreground border-border">
        {methodLabel(row.original.method)}
      </span>
    ),
  },
  {
    accessorKey: 'source',
    header: 'Source',
    cell: ({ row }) => (
      <span className="text-sm">{row.original.source || '—'}</span>
    ),
  },
]

export function ValuationRulesPage() {
  usePageTitle('Valuation Rules')
  const { manifestHistory } = useApiClients()

  const { data, isLoading, isError } = useQuery({
    queryKey: [...manifestKeys.current(), 'valuation-rules'],
    queryFn: () => manifestHistory.getCurrentManifest({}),
    staleTime: 60_000,
  })

  const valuationRules = data?.version?.manifest?.valuationRules ?? []

  const table = useReactTable({
    data: valuationRules,
    columns,
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <PageShell>
      <Breadcrumbs items={[
        { label: 'Economy', href: '/economy' },
        { label: 'Reference Data', href: '/reference-data' },
        { label: 'Valuation Rules' },
      ]} />

      <PageHeader
        title="Valuation Rules"
        description="Instrument conversion rules defining how assets are valued against each other."
      />

      <Card className="p-6">
        {isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 5 }).map((_, i) => (
              <div key={i} className="h-10 w-full animate-pulse rounded bg-muted" />
            ))}
          </div>
        ) : isError ? (
          <div className="flex h-32 items-center justify-center text-sm text-destructive">
            Failed to load valuation rules.
          </div>
        ) : valuationRules.length === 0 ? (
          <div
            data-testid="empty-state"
            className="flex h-32 flex-col items-center justify-center gap-2 text-muted-foreground"
          >
            <span className="text-sm font-medium">No valuation rules</span>
            <span className="text-xs">Define valuation rules in your economy manifest to see them here.</span>
          </div>
        ) : (
          <Table>
            <TableHeader>
              {table.getHeaderGroups().map((headerGroup) => (
                <TableRow key={headerGroup.id}>
                  {headerGroup.headers.map((header) => (
                    <TableHead key={header.id}>
                      {header.isPlaceholder
                        ? null
                        : flexRender(header.column.columnDef.header, header.getContext())}
                    </TableHead>
                  ))}
                </TableRow>
              ))}
            </TableHeader>
            <TableBody>
              {table.getRowModel().rows.map((row) => (
                <TableRow key={row.id}>
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
    </PageShell>
  )
}
