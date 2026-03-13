import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { EntityLink, PageShell, PageHeader } from '@/shared'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { AccountStatus } from '@/api/gen/meridian/current_account/v1/current_account_pb'
import { usePageTitle } from '@/hooks/use-page-title'
import { CreateAccountDialog } from './create-account-dialog'
import type { CurrentAccount } from './types'
import { useAccountsTable } from '../hooks'

const STATUS_OPTIONS = [
  { label: 'Active', value: String(AccountStatus.ACTIVE) },
  { label: 'Frozen', value: String(AccountStatus.FROZEN) },
  { label: 'Closed', value: String(AccountStatus.CLOSED) },
]

export function AccountsPage() {
  usePageTitle('Accounts')
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
      accessorKey: 'partyId',
      header: 'Party',
      cell: ({ row }) => row.original.partyId
        ? <span onClick={(e) => e.stopPropagation()}><EntityLink type="party" id={row.original.partyId} /></span>
        : <span className="text-muted-foreground">—</span>,
    },
    {
      accessorKey: 'createdAt',
      header: 'Created',
      cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} format="relative" />,
    },
  ]

  return (
    <PageShell>
      <PageHeader
        title="Accounts"
        description="Manage current accounts and their balances."
        actions={<Button onClick={() => setCreateOpen(true)}>Create Account</Button>}
      />

      <Card>
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
      </Card>

      <CreateAccountDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(accountId) => navigate(`/accounts/${accountId}`)}
      />
    </PageShell>
  )
}
