import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/components/shared/data-table'
import type { DataTableQueryParams, DataTableResult } from '@/components/shared/data-table'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { Button } from '@/components/ui/button'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { AccountStatus } from '@/api/gen/meridian/current_account/v1/current_account_pb'
import { CreateAccountDialog } from './create-account-dialog'
import type { CurrentAccount } from './types'

const STATUS_OPTIONS = [
  { label: 'Active', value: String(AccountStatus.ACTIVE) },
  { label: 'Frozen', value: String(AccountStatus.FROZEN) },
  { label: 'Closed', value: String(AccountStatus.CLOSED) },
]

const ACCOUNT_STATUS_NAMES: Record<number, string> = {
  [AccountStatus.ACTIVE]: 'ACTIVE',
  [AccountStatus.FROZEN]: 'FROZEN',
  [AccountStatus.CLOSED]: 'CLOSED',
}

export function AccountsPage() {
  const navigate = useNavigate()
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()
  const [createOpen, setCreateOpen] = React.useState(false)

  const columns: ColumnDef<CurrentAccount>[] = [
    {
      accessorKey: 'accountId',
      header: 'Account ID',
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

  const queryKey = React.useMemo(
    () => tenantKeys.accounts(tenantSlug ?? ''),
    [tenantSlug],
  )

  const queryFn = React.useCallback(
    async (params: DataTableQueryParams): Promise<DataTableResult<CurrentAccount>> => {
      if (!tenantSlug) return { items: [] }

      const statusFilter = params.filters?.status
      const parsedStatus =
        statusFilter !== undefined && statusFilter !== '' ? Number(statusFilter) : undefined
      const response = await clients.currentAccount.listCurrentAccounts({
        pageSize: params.pageSize,
        pageToken: params.pageToken ?? '',
        ...(parsedStatus !== undefined && Number.isFinite(parsedStatus)
          ? { status: parsedStatus as AccountStatus }
          : {}),
      })

      const accounts: CurrentAccount[] = (response.accounts ?? []).map((a) => ({
        accountId: a.accountId ?? '',
        externalReference: a.externalIdentifier ?? '',
        status: (ACCOUNT_STATUS_NAMES[a.accountStatus] ?? String(a.accountStatus)) as CurrentAccount['status'],
        instrumentCode: a.instrumentCode || '',
        availableBalance: '', // fetched separately via PositionKeeping; not in ListCurrentAccounts response
        createdAt: a.createdAt ?? undefined,
        updatedAt: a.updatedAt ?? undefined,
      }))

      return {
        items: accounts,
        nextPageToken: response.nextPageToken || undefined,
      }
    },
    [tenantSlug, clients],
  )

  return (
    <div className="p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Accounts</h1>
        <Button onClick={() => setCreateOpen(true)}>Create Account</Button>
      </div>

      <DataTable
        queryKey={queryKey}
        queryFn={queryFn}
        columns={columns}
        filters={[
          {
            field: 'status',
            label: 'Status',
            type: 'select',
            options: STATUS_OPTIONS,
          },
          {
            field: 'externalReference',
            label: 'External Ref',
            type: 'text',
          },
        ]}
        onRowClick={(row) => navigate(`/accounts/${row.accountId}`)}
      />

      <CreateAccountDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(accountId) => navigate(`/accounts/${accountId}`)}
      />
    </div>
  )
}
