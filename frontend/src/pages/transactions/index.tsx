import type { ColumnDef } from '@tanstack/react-table'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { DataTable, type DataTableQueryParams, type DataTableResult } from '@/components/shared/data-table'
import { TimeDisplay } from '@/components/shared/time-display'
import { DirectionBadge } from '@/components/shared/direction-badge'
import { EntityLink } from '@/components/shared/entity-link'
import { MoneyDisplay } from '@/components/shared/money-display'

interface LedgerPosting {
  id: string
  financialBookingLogId: string
  postingDirection: string
  amount: bigint | string
  currency: string
  accountId: string
  valueDate: { seconds: bigint | number; nanos?: number } | null | undefined
  createdAt: { seconds: bigint | number; nanos?: number } | null | undefined
  status: string
}

function getDirectionName(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number') {
    const directionMap: Record<number, string> = {
      0: 'UNSPECIFIED',
      1: 'DEBIT',
      2: 'CREDIT',
    }
    return directionMap[value] ?? String(value)
  }
  return String(value ?? '')
}

const columns: ColumnDef<LedgerPosting>[] = [
  {
    accessorKey: 'valueDate',
    header: 'Timestamp',
    enableSorting: true,
    cell: ({ row }) => (
      <TimeDisplay timestamp={row.original.valueDate} format="relative" />
    ),
  },
  {
    accessorKey: 'accountId',
    header: 'Account',
    cell: ({ row }) => (
      <EntityLink type="account" id={row.original.accountId} />
    ),
  },
  {
    accessorKey: 'postingDirection',
    header: 'Direction',
    cell: ({ row }) => <DirectionBadge direction={row.original.postingDirection} />,
  },
  {
    accessorKey: 'amount',
    header: 'Amount',
    enableSorting: true,
    cell: ({ row }) => (
      <MoneyDisplay amount={row.original.amount} currency={row.original.currency} />
    ),
  },
  {
    accessorKey: 'currency',
    header: 'Instrument',
  },
  {
    accessorKey: 'financialBookingLogId',
    header: 'Booking Log',
    cell: ({ row }) => (
      <EntityLink
        type="booking-log"
        id={row.original.financialBookingLogId}
        label={row.original.financialBookingLogId.slice(0, 8) + '…'}
      />
    ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => (
      <span className="text-sm text-muted-foreground">{row.original.status}</span>
    ),
  },
]

export function TransactionsPage() {
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()

  async function fetchPostings(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<LedgerPosting>> {
    if (!tenantSlug) return { items: [] }

    const directionFilter = params.filters?.postingDirection
    const accountFilter = params.filters?.accountId ?? ''

    const directionEnumMap: Record<string, number> = {
      DEBIT: 1,
      CREDIT: 2,
    }
    const postingDirection = directionFilter
      ? (directionEnumMap[directionFilter] as never)
      : (0 as never)

    const response = await clients.financialAccounting.listLedgerPostings({
      pagination: { pageSize: params.pageSize, pageToken: params.pageToken ?? '' },
      accountId: accountFilter,
      postingDirection,
    })

    const items = (response.ledgerPostings ?? []).map((p) => {
      const money = p.postingAmount as { currencyCode?: string; units?: bigint | string | number; nanos?: number } | null | undefined
      const currency = money?.currencyCode ?? ''
      const rawUnits = money?.units
      let amount: bigint | string = 0n
      if (rawUnits !== null && rawUnits !== undefined) {
        amount = typeof rawUnits === 'bigint' ? rawUnits : BigInt(String(rawUnits))
      }

      return {
        id: p.id ?? '',
        financialBookingLogId: p.financialBookingLogId ?? '',
        postingDirection: getDirectionName(p.postingDirection),
        amount,
        currency,
        accountId: p.accountId ?? '',
        valueDate: p.valueDate ?? null,
        createdAt: p.createdAt ?? null,
        status: getDirectionName(p.status),
      }
    }) as LedgerPosting[]

    const nextPageToken =
      typeof response.pagination?.nextPageToken === 'string'
        ? response.pagination.nextPageToken
        : undefined

    return {
      items,
      nextPageToken: nextPageToken || undefined,
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Transactions</h1>
        <p className="text-muted-foreground">
          Ledger postings across all accounts
        </p>
      </div>

      <DataTable<LedgerPosting>
        queryKey={[...(tenantSlug ? tenantKeys.transactions(tenantSlug) : ['no-tenant']), 'postings']}
        queryFn={fetchPostings}
        columns={columns}
        pageSize={25}
        filters={[
          {
            field: 'accountId',
            label: 'Account ID',
            type: 'text',
          },
          {
            field: 'postingDirection',
            label: 'Direction',
            type: 'select',
            options: [
              { label: 'Debit', value: 'DEBIT' },
              { label: 'Credit', value: 'CREDIT' },
            ],
          },
        ]}
      />
    </div>
  )
}
