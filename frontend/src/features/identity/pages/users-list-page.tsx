import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { UserPlus } from 'lucide-react'
import { useAuth } from '@/contexts/auth-context'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { Button } from '@/components/ui/button'
import type { Identity } from '@/api/gen/meridian/identity/v1/identity_pb'
import { IdentityStatus } from '@/api/gen/meridian/identity/v1/identity_pb'
import type { FilterConfig } from '@/shared/data-table'
import { useIdentities } from '../hooks/use-identities'
import { InviteDialog } from '../components/invite-dialog'

const STATUS_LABEL: Record<number, string> = {
  [IdentityStatus.UNSPECIFIED]: 'UNKNOWN',
  [IdentityStatus.PENDING_INVITE]: 'PENDING_INVITE',
  [IdentityStatus.ACTIVE]: 'ACTIVE',
  [IdentityStatus.SUSPENDED]: 'SUSPENDED',
  [IdentityStatus.LOCKED]: 'LOCKED',
}

const columns: ColumnDef<Identity>[] = [
  {
    accessorKey: 'email',
    header: 'Email',
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => {
      const statusLabel = STATUS_LABEL[row.original.status] ?? 'UNKNOWN'
      return <StatusBadge status={statusLabel} />
    },
  },
  {
    accessorKey: 'mfaEnabled',
    header: 'MFA',
    cell: ({ row }) => (
      <span className="text-sm text-muted-foreground">
        {row.original.mfaEnabled ? 'Enabled' : 'Disabled'}
      </span>
    ),
  },
  {
    accessorKey: 'lastLoginAt',
    header: 'Last Login',
    cell: ({ row }) =>
      row.original.lastLoginAt ? (
        <TimeDisplay timestamp={row.original.lastLoginAt} />
      ) : (
        <span className="text-sm text-muted-foreground">Never</span>
      ),
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} />,
  },
]

const statusFilterOptions: FilterConfig[] = [
  {
    field: 'statusFilter',
    label: 'Status',
    type: 'select',
    options: [
      { label: 'Active', value: String(IdentityStatus.ACTIVE) },
      { label: 'Pending Invite', value: String(IdentityStatus.PENDING_INVITE) },
      { label: 'Suspended', value: String(IdentityStatus.SUSPENDED) },
      { label: 'Locked', value: String(IdentityStatus.LOCKED) },
    ],
  },
]

export function UsersListPage() {
  const navigate = useNavigate()
  const { claims } = useAuth()
  const { queryKey, queryFn } = useIdentities()
  const [inviteOpen, setInviteOpen] = React.useState(false)
  const [tableKey, setTableKey] = React.useState(0)

  const currentUserRoles = claims?.roles ?? []

  function handleRowClick(row: Identity) {
    void navigate(`/users/${row.id}`)
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Users</h1>
        <Button onClick={() => setInviteOpen(true)}>
          <UserPlus className="mr-2 size-4" />
          Invite User
        </Button>
      </div>

      <DataTable<Identity>
        key={tableKey}
        queryKey={queryKey}
        queryFn={queryFn}
        columns={columns}
        pageSize={25}
        filters={statusFilterOptions}
        onRowClick={handleRowClick}
        emptyState={
          <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
            <span className="text-sm font-medium">No users found</span>
            <span className="text-xs">Invite a user to get started.</span>
          </div>
        }
      />

      <InviteDialog
        open={inviteOpen}
        onOpenChange={(open) => {
          setInviteOpen(open)
          if (!open) setTableKey((k) => k + 1)
        }}
        currentUserRoles={currentUserRoles}
      />
    </div>
  )
}
