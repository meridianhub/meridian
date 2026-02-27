import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/components/shared/data-table'
import type { DataTableQueryParams, DataTableResult } from '@/components/shared/data-table'
import { EntityLink } from '@/components/shared/entity-link'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { useClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { AccountStatus } from '@/api/gen/meridian/current_account/v1/current_account_pb'

interface AccountsTabProps {
  partyId: string
}

interface AccountRow {
  accountId: string
  externalReference: string
  status: string
  instrumentCode: string
  createdAt?: { seconds: number | bigint; nanos?: number }
}

const ACCOUNT_STATUS_NAMES: Record<number, string> = {
  [AccountStatus.ACTIVE]: 'ACTIVE',
  [AccountStatus.FROZEN]: 'FROZEN',
  [AccountStatus.CLOSED]: 'CLOSED',
}

const columns: ColumnDef<AccountRow>[] = [
  {
    accessorKey: 'accountId',
    header: 'Account ID',
    cell: ({ row }) => (
      <EntityLink type="account" id={row.original.accountId} />
    ),
  },
  {
    accessorKey: 'externalReference',
    header: 'External Ref',
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'instrumentCode',
    header: 'Instrument',
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} format="relative" />,
  },
]

export function AccountsTab({ partyId }: AccountsTabProps) {
  const clients = useClients()
  const tenantSlug = useTenantSlug()
  const navigate = useNavigate()

  const queryKey = React.useMemo(
    () => [...tenantKeys.party(tenantSlug ?? '', partyId), 'accounts'],
    [tenantSlug, partyId],
  )

  const queryFn = React.useCallback(
    async (params: DataTableQueryParams): Promise<DataTableResult<AccountRow>> => {
      if (!tenantSlug) return { items: [] }

      // The API does not support filtering by partyId, so we fetch pages and filter
      // client-side. We continue fetching until we have enough matching rows to fill
      // pageSize, or the API is exhausted. MAX_PAGES caps sequential API calls to
      // prevent unbounded fetching when the party owns few accounts in a large dataset.
      const MAX_PAGES = 10
      const collected: AccountRow[] = []
      let cursor = params.pageToken ?? ''
      let nextPageToken: string | undefined
      let pagesScanned = 0

      while (collected.length < params.pageSize && pagesScanned < MAX_PAGES) {
        pagesScanned++
        // Use remaining slots as batch size to avoid dropping same-page overflow:
        // if the batch were larger than remaining slots, we might get more matches
        // than pageSize in one batch but nextPageToken would advance past them.
        const remaining = Math.max(params.pageSize - collected.length, 1)
        const response = await clients.currentAccount.listCurrentAccounts({
          pageSize: remaining,
          pageToken: cursor,
        })

        for (const a of response.accounts ?? []) {
          if (a.orgPartyId === partyId) {
            collected.push({
              accountId: a.accountId,
              externalReference: a.externalIdentifier ?? '',
              status: ACCOUNT_STATUS_NAMES[a.accountStatus] ?? String(a.accountStatus),
              instrumentCode: a.instrumentCode || '',
              createdAt: a.createdAt ?? undefined,
            })
          }
        }

        if (!response.nextPageToken) {
          nextPageToken = undefined
          break
        }

        cursor = response.nextPageToken
        nextPageToken = response.nextPageToken
      }

      return {
        items: collected.slice(0, params.pageSize),
        nextPageToken,
      }
    },
    [tenantSlug, partyId, clients],
  )

  return (
    <DataTable
      queryKey={queryKey}
      queryFn={queryFn}
      columns={columns}
      onRowClick={(row) => navigate(`/accounts/${row.accountId}`)}
    />
  )
}
