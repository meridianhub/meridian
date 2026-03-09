import * as React from 'react'
import { Role } from '@/api/gen/meridian/identity/v1/identity_pb'
import type { RoleAssignment } from '@/api/gen/meridian/identity/v1/identity_pb'
import { Button } from '@/components/ui/button'
import { TimeDisplay } from '@/shared/time-display'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { useGrantRole, useRevokeRole } from '../hooks/use-identities'
import { ROLE_LABELS, getGrantableRoles } from '../lib/roles'

interface RoleManagementProps {
  identityId: string
  roleAssignments: RoleAssignment[]
  currentUserRoles: string[]
}

export function RoleManagement({
  identityId,
  roleAssignments,
  currentUserRoles,
}: RoleManagementProps) {
  const grantRole = useGrantRole()
  const revokeRole = useRevokeRole()
  const [grantDialogOpen, setGrantDialogOpen] = React.useState(false)
  const [selectedRole, setSelectedRole] = React.useState<Role>(Role.OPERATOR)

  const grantableRoles = getGrantableRoles(currentUserRoles)
  const canGrant = grantableRoles.length > 0
  const activeAssignments = roleAssignments.filter((ra) => !ra.revoked)

  async function handleGrantRole() {
    try {
      await grantRole.mutateAsync({ identityId, role: selectedRole })
      setGrantDialogOpen(false)
    } catch {
      // error handled by mutation state
    }
  }

  async function handleRevokeRole(roleAssignmentId: string) {
    try {
      await revokeRole.mutateAsync({ identityId, roleAssignmentId })
    } catch {
      // error handled by mutation state
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-medium">Roles</h3>
        {canGrant && (
          <Button size="sm" onClick={() => setGrantDialogOpen(true)}>
            Grant Role
          </Button>
        )}
      </div>

      {activeAssignments.length === 0 ? (
        <p className="text-sm text-muted-foreground">No roles assigned.</p>
      ) : (
        <div className="rounded-md border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-2 text-left font-medium">Role</th>
                <th className="px-4 py-2 text-left font-medium">Granted</th>
                <th className="px-4 py-2 text-left font-medium">Expires</th>
                <th className="px-4 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {activeAssignments.map((ra) => (
                <tr key={ra.id} className="border-b last:border-0">
                  <td className="px-4 py-2">{ROLE_LABELS[ra.role] ?? 'Unknown'}</td>
                  <td className="px-4 py-2">
                    <TimeDisplay timestamp={ra.grantedAt} />
                  </td>
                  <td className="px-4 py-2">
                    {ra.expiresAt ? <TimeDisplay timestamp={ra.expiresAt} /> : 'Never'}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {canGrant && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => void handleRevokeRole(ra.id)}
                        disabled={revokeRole.isPending}
                      >
                        Revoke
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Dialog open={grantDialogOpen} onOpenChange={setGrantDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Grant Role</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-1">
              <label htmlFor="grant-role-select" className="text-sm font-medium">
                Role
              </label>
              <select
                id="grant-role-select"
                value={selectedRole}
                onChange={(e) => setSelectedRole(Number(e.target.value) as Role)}
                className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
              >
                {grantableRoles.map((r) => (
                  <option key={r} value={r}>
                    {ROLE_LABELS[r]}
                  </option>
                ))}
              </select>
            </div>
            {grantRole.error && (
              <p className="text-sm text-destructive">Failed to grant role. Please try again.</p>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setGrantDialogOpen(false)}>
              Cancel
            </Button>
            <Button onClick={() => void handleGrantRole()} disabled={grantRole.isPending}>
              {grantRole.isPending ? 'Granting...' : 'Grant Role'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
