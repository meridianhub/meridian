import * as React from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { useNavigate } from 'react-router-dom'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay, PageShell, PageHeader } from '@/shared'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { useInternalAccountsTable } from '../hooks'
import { CreateInternalAccountDialog } from './create-internal-account-dialog'

interface InternalAccountRow {
  accountId: string
  accountCode: string
  name: string
  behaviorClass: string
  accountStatus: number
  instrumentCode: string
  createdAt?: { seconds: bigint | number; nanos?: number } | null
}

function accountStatusLabel(status: number): string {
  switch (status) {
    case 1:
      return 'ACTIVE'
    case 2:
      return 'SUSPENDED'
    case 3:
      return 'CLOSED'
    default:
      return 'UNKNOWN'
  }
}

const BEHAVIOR_CLASS_OPTIONS = [
  { label: 'Clearing', value: 'CLEARING' },
  { label: 'Nostro', value: 'NOSTRO' },
  { label: 'Vostro', value: 'VOSTRO' },
  { label: 'Holding', value: 'HOLDING' },
  { label: 'Suspense', value: 'SUSPENSE' },
  { label: 'Revenue', value: 'REVENUE' },
  { label: 'Expense', value: 'EXPENSE' },
]

const STATUS_OPTIONS = [
  { label: 'Active', value: '1' },
  { label: 'Suspended', value: '2' },
  { label: 'Closed', value: '3' },
]

const columns: ColumnDef<InternalAccountRow>[] = [
  {
    accessorKey: 'accountCode',
    header: 'Account Code',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.accountCode}</span>
    ),
  },
  {
    accessorKey: 'name',
    header: 'Name',
  },
  {
    accessorKey: 'behaviorClass',
    header: 'Type',
    cell: ({ row }) => (
      <span className="text-sm text-muted-foreground">{row.original.behaviorClass}</span>
    ),
  },
  {
    accessorKey: 'instrumentCode',
    header: 'Instrument',
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.original.instrumentCode}</span>
    ),
  },
  {
    accessorKey: 'accountStatus',
    header: 'Status',
    cell: ({ row }) => (
      <StatusBadge status={accountStatusLabel(row.original.accountStatus)} />
    ),
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} format="relative" />,
  },
]

export function InternalAccountsPage() {
  const { queryKey, queryFn, tenantSlug } = useInternalAccountsTable()
  const navigate = useNavigate()
  const [createDialogOpen, setCreateDialogOpen] = React.useState(false)

  if (!tenantSlug) {
    return (
      <PageShell>
        <p className="text-muted-foreground">No tenant selected.</p>
      </PageShell>
    )
  }

  return (
    <PageShell>
      <PageHeader
        title="Internal Accounts"
        description="Operational accounts including clearing, nostro, vostro, and holding accounts."
        actions={
          <Button onClick={() => setCreateDialogOpen(true)}>New Internal Account</Button>
        }
      />

      <CreateInternalAccountDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
      />

      <Card>
        <DataTable<InternalAccountRow>
          queryKey={queryKey}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          filters={[
            {
              field: 'behaviorClass',
              label: 'Type',
              type: 'select',
              options: BEHAVIOR_CLASS_OPTIONS,
            },
            {
              field: 'status',
              label: 'Status',
              type: 'select',
              options: STATUS_OPTIONS,
            },
          ]}
          onRowClick={(row) => navigate(`/internal-accounts/${row.accountId}`)}
        />
      </Card>
    </PageShell>
  )
}
