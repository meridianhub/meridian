import * as React from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { useNavigate } from 'react-router-dom'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { Button } from '@/components/ui/button'
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
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()
  const navigate = useNavigate()
  const [createDialogOpen, setCreateDialogOpen] = React.useState(false)

  if (!tenantSlug) {
    return (
      <div className="p-6">
        <p className="text-muted-foreground">No tenant selected.</p>
      </div>
    )
  }

  return (
    <div className="p-6">
      <div className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Internal Accounts</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Operational accounts including clearing, nostro, vostro, and holding accounts.
          </p>
        </div>
        <Button onClick={() => setCreateDialogOpen(true)}>New Internal Account</Button>
      </div>

      <CreateInternalAccountDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
      />

      <DataTable<InternalAccountRow>
        queryKey={tenantKeys.internalAccounts(tenantSlug)}
        queryFn={async ({ pageToken, pageSize, filters }) => {
          const statusFilter = filters?.status ? parseInt(filters.status, 10) : 0
          const res = await clients.internalAccount.listInternalAccounts({
            behaviorClassFilter: filters?.behaviorClass ?? '',
            statusFilter,
            pagination: { pageToken: pageToken ?? '', pageSize },
          })
          return {
            items: res.facilities.map((f) => ({
              accountId: f.accountId,
              accountCode: f.accountCode,
              name: f.name,
              behaviorClass: f.behaviorClass,
              accountStatus: f.accountStatus,
              instrumentCode: f.instrumentCode,
              createdAt: f.createdAt ?? null,
            })),
            nextPageToken: res.pagination?.nextPageToken || undefined,
          }
        }}
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
    </div>
  )
}
