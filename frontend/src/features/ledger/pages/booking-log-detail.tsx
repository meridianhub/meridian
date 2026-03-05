import { useParams } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay, EntityLink, Breadcrumbs } from '@/shared'
import { MoneyDisplay } from '@/shared/money-display'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useBookingLogDetail } from '../hooks'
import { BalanceIndicator } from './balance-indicator'
import { DirectionBadge } from './direction-badge'
import { BookingLogHeader } from './booking-log-header'
import type { LedgerPosting } from './types'

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
      <StatusBadge status={row.original.status} />
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

  const { data, isLoading, isError } = useBookingLogDetail(bookingLogId)

  const postings = data?.postings ?? []
  const postingCurrency = postings.find((p) => p.postingAmount?.currencyCode)?.postingAmount?.currencyCode
  const currency = data?.instrumentCode || postingCurrency || ''
  const { debitTotal, creditTotal } = computeTotals(postings, currency)

  if (isLoading) {
    return (
      <div className="space-y-6">
        <Breadcrumbs items={[{ label: 'Ledger', href: '/ledger' }, { label: 'Loading...' }]} />
        <div className="h-32 animate-pulse rounded-lg bg-muted" />
      </div>
    )
  }

  if (isError || !data) {
    return (
      <div className="space-y-6">
        <Breadcrumbs items={[{ label: 'Ledger', href: '/ledger' }, { label: 'Error' }]} />
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700">
          Failed to load booking log. Please try again.
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Breadcrumb navigation */}
      <Breadcrumbs
        items={[
          { label: 'Ledger', href: '/ledger' },
          { label: data.id },
        ]}
      />

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
