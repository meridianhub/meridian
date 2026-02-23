import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/components/shared/data-table'
import type { DataTableQueryParams, DataTableResult } from '@/components/shared/data-table'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { Button } from '@/components/ui/button'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { CreateAccountDialog } from './create-account-dialog'
import type { CurrentAccount } from './types'

const STATUS_OPTIONS = [
  { label: 'Active', value: 'ACCOUNT_STATUS_ACTIVE' },
  { label: 'Frozen', value: 'ACCOUNT_STATUS_FROZEN' },
  { label: 'Closed', value: 'ACCOUNT_STATUS_CLOSED' },
]

async function listAccounts(
  tenantSlug: string,
  params: DataTableQueryParams,
): Promise<DataTableResult<CurrentAccount>> {
  const body: Record<string, unknown> = {
    pageSize: params.pageSize,
  }
  if (params.pageToken) {
    body.pageToken = params.pageToken
  }
  if (params.filters?.status) {
    body.status = params.filters.status
  }
  if (params.filters?.iban) {
    body.iban = params.filters.iban
  }

  const response = await fetch(
    `/api/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-Slug': tenantSlug,
      },
      body: JSON.stringify(body),
    },
  )

  if (!response.ok) {
    throw new Error(`Failed to list accounts: ${response.status}`)
  }

  const data = (await response.json()) as RawListCurrentAccountsResponse

  // Map proto CurrentAccountFacility fields to frontend CurrentAccount shape
  const accounts: CurrentAccount[] = (data.accounts ?? []).map((a) => ({
    accountId: a.accountId ?? '',
    iban: a.accountIdentification ?? '',
    status: stripEnumPrefix(a.accountStatus ?? '', 'ACCOUNT_STATUS_') as CurrentAccount['status'],
    baseCurrency: stripEnumPrefix(a.baseCurrency ?? '', 'CURRENCY_'),
    availableBalance: '',
    createdAt: a.createdAt,
    updatedAt: a.updatedAt,
  }))

  return {
    items: accounts,
    nextPageToken: data.nextPageToken || undefined,
  }
}

// Strip proto enum prefix (e.g., "ACCOUNT_STATUS_ACTIVE" -> "ACTIVE")
function stripEnumPrefix(value: string, prefix: string): string {
  return value.startsWith(prefix) ? value.slice(prefix.length) : value
}

// Raw proto response shape before mapping
interface RawListCurrentAccountsResponse {
  accounts?: Array<{
    accountId?: string
    accountIdentification?: string
    accountStatus?: string
    baseCurrency?: string
    createdAt?: { seconds: number | bigint; nanos?: number }
    updatedAt?: { seconds: number | bigint; nanos?: number }
  }>
  nextPageToken?: string
  totalCount?: string
}

export function AccountsPage() {
  const navigate = useNavigate()
  const { tenantSlug } = useTenantContext()
  const [createOpen, setCreateOpen] = React.useState(false)

  const columns: ColumnDef<CurrentAccount>[] = [
    {
      accessorKey: 'accountId',
      header: 'Account ID',
    },
    {
      accessorKey: 'iban',
      header: 'IBAN',
    },
    {
      accessorKey: 'status',
      header: 'Status',
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'baseCurrency',
      header: 'Currency',
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
    (params: DataTableQueryParams) => listAccounts(tenantSlug ?? '', params),
    [tenantSlug],
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
            field: 'iban',
            label: 'IBAN',
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
