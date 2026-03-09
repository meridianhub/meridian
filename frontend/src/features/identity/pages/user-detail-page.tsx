import * as React from 'react'
import { useParams, Link } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { useAuth } from '@/contexts/auth-context'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { DetailSkeleton } from '@/shared/detail-skeleton'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { IdentityStatus } from '@/api/gen/meridian/identity/v1/identity_pb'
import {
  useIdentity,
  useIdentityRoles,
  useSuspendIdentity,
  useReactivateIdentity,
} from '../hooks/use-identities'
import { RoleManagement } from '../components/role-management'

const STATUS_LABEL: Record<number, string> = {
  [IdentityStatus.UNSPECIFIED]: 'UNKNOWN',
  [IdentityStatus.PENDING_INVITE]: 'PENDING_INVITE',
  [IdentityStatus.ACTIVE]: 'ACTIVE',
  [IdentityStatus.SUSPENDED]: 'SUSPENDED',
  [IdentityStatus.LOCKED]: 'LOCKED',
}

export function UserDetailPage() {
  const { userId } = useParams<{ userId: string }>()
  const { claims } = useAuth()
  const { data: identity, isLoading, isError } = useIdentity(userId ?? '')
  const { data: roleAssignments } = useIdentityRoles(userId ?? '')
  const suspendIdentity = useSuspendIdentity()
  const reactivateIdentity = useReactivateIdentity()

  const [actionDialogOpen, setActionDialogOpen] = React.useState(false)
  const [actionType, setActionType] = React.useState<'suspend' | 'reactivate'>('suspend')
  const [reason, setReason] = React.useState('')

  const currentUserRoles = claims?.roles ?? []

  if (isLoading) return <DetailSkeleton />

  if (isError || !identity) {
    return (
      <div className="p-6">
        <Link to="/users" className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground mb-4">
          <ArrowLeft className="size-4" />
          Back to Users
        </Link>
        <p className="text-destructive">Failed to load user details.</p>
      </div>
    )
  }

  const statusLabel = STATUS_LABEL[identity.status] ?? 'UNKNOWN'
  const canSuspend = identity.status === IdentityStatus.ACTIVE
  const canReactivate =
    identity.status === IdentityStatus.SUSPENDED || identity.status === IdentityStatus.LOCKED

  function openAction(type: 'suspend' | 'reactivate') {
    setActionType(type)
    setReason('')
    setActionDialogOpen(true)
  }

  async function handleAction() {
    if (!reason.trim() || !userId) return
    try {
      if (actionType === 'suspend') {
        await suspendIdentity.mutateAsync({ id: userId, reason })
      } else {
        await reactivateIdentity.mutateAsync({ id: userId, reason })
      }
      setActionDialogOpen(false)
    } catch {
      // error handled by mutation state
    }
  }

  const isPending = suspendIdentity.isPending || reactivateIdentity.isPending

  return (
    <div className="p-6 space-y-6">
      <Link to="/users" className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" />
        Back to Users
      </Link>

      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{identity.email}</h1>
          <p className="mt-1 text-sm text-muted-foreground">ID: {identity.id}</p>
        </div>
        <div className="flex items-center gap-2">
          {canSuspend && (
            <Button variant="destructive" size="sm" onClick={() => openAction('suspend')}>
              Suspend
            </Button>
          )}
          {canReactivate && (
            <Button variant="outline" size="sm" onClick={() => openAction('reactivate')}>
              Reactivate
            </Button>
          )}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <div className="rounded-lg border p-4 space-y-3">
          <h2 className="text-lg font-medium">Identity Details</h2>
          <dl className="space-y-2 text-sm">
            <div className="flex justify-between">
              <dt className="text-muted-foreground">Status</dt>
              <dd><StatusBadge status={statusLabel} /></dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-muted-foreground">MFA</dt>
              <dd>{identity.mfaEnabled ? 'Enabled' : 'Disabled'}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-muted-foreground">Last Login</dt>
              <dd>
                {identity.lastLoginAt ? (
                  <TimeDisplay timestamp={identity.lastLoginAt} />
                ) : (
                  'Never'
                )}
              </dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-muted-foreground">Created</dt>
              <dd><TimeDisplay timestamp={identity.createdAt} /></dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-muted-foreground">Updated</dt>
              <dd><TimeDisplay timestamp={identity.updatedAt} /></dd>
            </div>
            {identity.failedAttempts > 0 && (
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Failed Attempts</dt>
                <dd className="text-destructive">{identity.failedAttempts}</dd>
              </div>
            )}
            {identity.lockedUntil && (
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Locked Until</dt>
                <dd><TimeDisplay timestamp={identity.lockedUntil} /></dd>
              </div>
            )}
            {identity.externalIdp && (
              <div className="flex justify-between">
                <dt className="text-muted-foreground">External IdP</dt>
                <dd className="truncate max-w-48">{identity.externalIdp}</dd>
              </div>
            )}
          </dl>
        </div>

        <div className="rounded-lg border p-4">
          <RoleManagement
            identityId={identity.id}
            roleAssignments={roleAssignments ?? []}
            currentUserRoles={currentUserRoles}
          />
        </div>
      </div>

      <Dialog open={actionDialogOpen} onOpenChange={setActionDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {actionType === 'suspend' ? 'Suspend User' : 'Reactivate User'}
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <p className="text-sm text-muted-foreground">
              {actionType === 'suspend'
                ? 'Suspending this user will prevent them from logging in.'
                : 'Reactivating this user will restore their access.'}
            </p>
            <div className="space-y-1">
              <label htmlFor="action-reason" className="text-sm font-medium">
                Reason
              </label>
              <Input
                id="action-reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder="Provide a reason for this action"
              />
            </div>
            {(suspendIdentity.error || reactivateIdentity.error) && (
              <p className="text-sm text-destructive">
                Action failed. Please try again.
              </p>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setActionDialogOpen(false)}>
              Cancel
            </Button>
            <Button
              variant={actionType === 'suspend' ? 'destructive' : 'default'}
              onClick={() => void handleAction()}
              disabled={isPending || !reason.trim()}
            >
              {isPending
                ? 'Processing...'
                : actionType === 'suspend'
                  ? 'Suspend User'
                  : 'Reactivate User'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
