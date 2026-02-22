import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/components/shared/data-table'
import type { DataTableQueryParams, DataTableResult } from '@/components/shared/data-table'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { CurrentAccount, ListCurrentAccountsResponse } from './types'

const STATUS_OPTIONS = [
  { label: 'Active', value: 'ACTIVE' },
  { label: 'Frozen', value: 'FROZEN' },
  { label: 'Closed', value: 'CLOSED' },
  { label: 'Suspended', value: 'SUSPENDED' },
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

  const data = (await response.json()) as ListCurrentAccountsResponse
  return {
    items: data.accounts ?? [],
    nextPageToken: data.nextPageToken || undefined,
  }
}

export function AccountsPage() {
  const navigate = useNavigate()
  const { tenantSlug } = useTenantContext()

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
    </div>
  )
}
