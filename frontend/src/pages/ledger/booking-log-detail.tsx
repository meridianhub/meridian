import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay, EntityLink } from '@/components/shared'
import { MoneyDisplay } from '@/components/shared/money-display'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { BalanceIndicator } from './balance-indicator'
import { DirectionBadge } from './direction-badge'
import { BookingLogHeader } from './booking-log-header'
import type { LedgerPosting, FinancialBookingLog } from './types'

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

function getDirectionName(direction: unknown): string {
  if (typeof direction === 'string') return direction
  if (typeof direction === 'number') {
    const dirMap: Record<number, string> = {
      0: 'UNSPECIFIED',
      1: 'DEBIT',
      2: 'CREDIT',
    }
    return dirMap[direction] ?? String(direction)
  }
  return String(direction ?? '')
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

function computeTotals(postings: LedgerPosting[], _currency: string): { debitTotal: bigint; creditTotal: bigint } {
  let debitTotal = 0n
  let creditTotal = 0n

  for (const posting of postings) {
    const direction = getDirectionName(posting.postingDirection)
    const units = posting.postingAmount?.units
    const amount = typeof units === 'bigint' ? units : typeof units === 'number' && Number.isSafeInteger(units) ? BigInt(units) : 0n

    if (direction === 'DEBIT') {
      debitTotal += amount
    } else if (direction === 'CREDIT') {
      creditTotal += amount
    }
  }

  return { debitTotal, creditTotal }
}

const postingColumns: ColumnDef<LedgerPosting>[] = [
  {
    accessorKey: 'id',
    header: 'Posting ID',
    cell: ({ row }) => (
      <span className="font-mono text-xs">{row.original.id}</span>
    ),
  },
  {
    accessorKey: 'postingDirection',
    header: 'Direction',
    cell: ({ row }) => (
      <DirectionBadge direction={getDirectionName(row.original.postingDirection)} />
    ),
  },
  {
    accessorKey: 'postingAmount',
    header: 'Amount',
    cell: ({ row }) => {
      const amount = row.original.postingAmount
      const rawUnits = amount?.units
      const units = typeof rawUnits === 'bigint' ? rawUnits : typeof rawUnits === 'number' && Number.isSafeInteger(rawUnits) ? BigInt(rawUnits) : 0n
      const currency = amount?.currencyCode ?? ''
      return <MoneyDisplay amount={units} currency={currency} />
    },
  },
  {
    accessorKey: 'accountId',
    header: 'Account',
    cell: ({ row }) => (
      <EntityLink type="account" id={row.original.accountId} />
    ),
  },
  {
    accessorKey: 'valueDate',
    header: 'Value Date',
    cell: ({ row }) => (
      <TimeDisplay timestamp={row.original.valueDate} format="absolute" />
    ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => (
      <StatusBadge status={getStatusName(row.original.status)} />
    ),
  },
]

function PostingsTable({ postings }: { postings: LedgerPosting[] }) {
  const table = useReactTable({
    data: postings,
    columns: postingColumns,
    getCoreRowModel: getCoreRowModel(),
  })

  if (postings.length === 0) {
    return (
      <div className="flex h-24 items-center justify-center text-sm text-muted-foreground">
        No postings found for this booking log.
      </div>
    )
  }

  return (
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
  )
}

export function BookingLogDetailPage() {
  const { bookingLogId } = useParams<{ bookingLogId: string }>()
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()

  const { data, isLoading, isError } = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'ledger', 'bookingLog', bookingLogId],
    queryFn: async () => {
      const response = await clients.financialAccounting.retrieveFinancialBookingLog({
        id: bookingLogId ?? '',
      })

      const log = response.financialBookingLog
      if (!log) return null

      const postings: LedgerPosting[] = (log.postings ?? []).map((p) => ({
        id: p.id,
        financialBookingLogId: p.financialBookingLogId,
        postingDirection: getDirectionName(p.postingDirection),
        postingAmount: {
          currencyCode: typeof p.postingAmount?.currencyCode === 'string'
            ? p.postingAmount.currencyCode
            : '',
          units: (() => {
            const u = p.postingAmount?.units
            return typeof u === 'bigint' ? u : typeof u === 'number' && Number.isSafeInteger(u) ? BigInt(u) : 0n
          })(),
          nanos: p.postingAmount?.nanos ?? 0,
        },
        accountId: p.accountId,
        valueDate: p.valueDate ?? null,
        postingResult: p.postingResult ?? '',
        createdAt: p.createdAt ?? null,
        status: getStatusName(p.status),
      }))

      const bookingLog: FinancialBookingLog = {
        id: log.id,
        financialAccountType: String(log.financialAccountType ?? ''),
        productServiceReference: String(log.productServiceReference ?? ''),
        businessUnitReference: String(log.businessUnitReference ?? ''),
        chartOfAccountsRules: String(log.chartOfAccountsRules ?? ''),
        baseCurrency: getCurrencyName(log.baseCurrency),
        status: getStatusName(log.status),
        createdAt: log.createdAt ?? null,
        updatedAt: log.updatedAt ?? null,
        postings,
      }

      return bookingLog
    },
    enabled: !!tenantSlug && !!bookingLogId,
  })

  const postings = data?.postings ?? []
  const currency = data?.baseCurrency ?? 'GBP'
  const { debitTotal, creditTotal } = computeTotals(postings, currency)

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-2">
          <Link to="/ledger" className="text-sm text-muted-foreground hover:underline">
            Ledger
          </Link>
          <span className="text-muted-foreground">/</span>
          <span className="text-sm">Loading...</span>
        </div>
        <div className="h-32 animate-pulse rounded-lg bg-muted" />
      </div>
    )
  }

  if (isError || !data) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-2">
          <Link to="/ledger" className="text-sm text-muted-foreground hover:underline">
            Ledger
          </Link>
          <span className="text-muted-foreground">/</span>
          <span className="text-sm">Error</span>
        </div>
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700">
          Failed to load booking log. Please try again.
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Breadcrumb navigation */}
      <div className="flex items-center gap-2">
        <Link to="/ledger" className="text-sm text-muted-foreground hover:underline">
          Ledger
        </Link>
        <span className="text-muted-foreground">/</span>
        <span className="font-mono text-sm">{data.id}</span>
      </div>

      {/* Booking log header with metadata */}
      <BookingLogHeader bookingLog={data} />

      {/* Balance validation indicator */}
      {postings.length > 0 && (
        <BalanceIndicator
          debitTotal={debitTotal}
          creditTotal={creditTotal}
          currency={currency}
        />
      )}

      {/* Postings table */}
      <Card>
        <CardHeader>
          <CardTitle>Ledger Postings</CardTitle>
        </CardHeader>
        <CardContent>
          <PostingsTable postings={postings} />
        </CardContent>
      </Card>
    </div>
  )
}
