import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { useClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { InternalAccountStatus } from '@/api/gen/meridian/internal_account/v1/internal_account_pb'

interface InternalAccountsTabProps {
  partyId: string
}

interface InternalAccountRow {
  accountId: string
  accountCode: string
  name: string
  behaviorClass: string
  accountStatus: InternalAccountStatus
  instrumentCode: string
}

function accountStatusLabel(status: InternalAccountStatus): string {
  switch (status) {
    case InternalAccountStatus.ACTIVE:
      return 'ACTIVE'
    case InternalAccountStatus.SUSPENDED:
      return 'SUSPENDED'
    case InternalAccountStatus.CLOSED:
      return 'CLOSED'
    default:
      return 'UNKNOWN'
  }
}

const columns: ColumnDef<InternalAccountRow>[] = [
  {
    accessorKey: 'accountCode',
    header: 'Account Code',
  },
  {
    accessorKey: 'name',
    header: 'Name',
  },
  {
    accessorKey: 'behaviorClass',
    header: 'Type',
  },
  {
    accessorKey: 'instrumentCode',
    header: 'Instrument',
  },
  {
    accessorKey: 'accountStatus',
    header: 'Status',
    cell: ({ row }) => <StatusBadge status={accountStatusLabel(row.original.accountStatus)} />,
  },
]

export function InternalAccountsTab({ partyId }: InternalAccountsTabProps) {
  const clients = useClients()
  const tenantSlug = useTenantSlug()
  const navigate = useNavigate()

  const queryKey = React.useMemo(
    () => [...tenantKeys.party(tenantSlug ?? '', partyId), 'internal-accounts'],
    [tenantSlug, partyId],
  )

  const queryFn = React.useCallback(
    async (params: DataTableQueryParams): Promise<DataTableResult<InternalAccountRow>> => {
      if (!tenantSlug) return { items: [] }

      const res = await clients.internalAccount.listInternalAccounts({
        orgPartyIdFilter: partyId,
        pagination: { pageToken: params.pageToken ?? '', pageSize: params.pageSize },
      })

      return {
        items: res.facilities.map((f) => ({
          accountId: f.accountId,
          accountCode: f.accountCode,
          name: f.name,
          behaviorClass: f.behaviorClass,
          accountStatus: f.accountStatus,
          instrumentCode: f.instrumentCode,
        })),
        nextPageToken: res.pagination?.nextPageToken || undefined,
      }
    },
    [tenantSlug, partyId, clients],
  )

  return (
    <DataTable
      queryKey={queryKey}
      queryFn={queryFn}
      columns={columns}
      onRowClick={(row) => navigate(`/internal-accounts/${row.accountId}`)}
    />
  )
}
