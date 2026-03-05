import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { Button } from '@/components/ui/button'
import { AccountStatus } from '@/api/gen/meridian/current_account/v1/current_account_pb'
import { CreateAccountDialog } from './create-account-dialog'
import type { CurrentAccount } from './types'
import { useAccountsTable } from '../hooks'

const STATUS_OPTIONS = [
  { label: 'Active', value: String(AccountStatus.ACTIVE) },
  { label: 'Frozen', value: String(AccountStatus.FROZEN) },
  { label: 'Closed', value: String(AccountStatus.CLOSED) },
]

export function AccountsPage() {
  const navigate = useNavigate()
  const [createOpen, setCreateOpen] = React.useState(false)
  const { queryKey, queryFn } = useAccountsTable()

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
