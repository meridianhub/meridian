import * as React from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Role } from '@/api/gen/meridian/identity/v1/identity_pb'
import { useInviteUser } from '../hooks/use-identities'
import { ROLE_LABELS, getGrantableRoles } from '../lib/roles'

interface InviteDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentUserRoles: string[]
}

export function InviteDialog({ open, onOpenChange, currentUserRoles }: InviteDialogProps) {
  const inviteUser = useInviteUser()
  const [email, setEmail] = React.useState('')
  const [role, setRole] = React.useState<Role>(Role.OPERATOR)
  const [errors, setErrors] = React.useState<{ email?: string }>({})

  const grantableRoles = getGrantableRoles(currentUserRoles)

  React.useEffect(() => {
    if (!open) {
      setEmail('')
      setRole(Role.OPERATOR)
      setErrors({})
    }
  }, [open])

  function validate(): boolean {
    const newErrors: { email?: string } = {}
    if (!email.trim()) {
      newErrors.email = 'Email is required'
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      newErrors.email = 'Invalid email address'
    }
    setErrors(newErrors)
    return Object.keys(newErrors).length === 0
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return

    try {
      await inviteUser.mutateAsync({ email, role })
      onOpenChange(false)
    } catch {
      // error handled by mutation state
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Invite User</DialogTitle>
        </DialogHeader>
        <form onSubmit={(e) => void handleSubmit(e)} id="invite-user-form">
          <div className="space-y-4 py-2">
            <div className="space-y-1">
              <label htmlFor="invite-email" className="text-sm font-medium">
                Email
              </label>
              <Input
                id="invite-email"
                type="email"
                value={email}
                onChange={(e) => {
                  setEmail(e.target.value)
                  if (errors.email) setErrors({})
                }}
                placeholder="user@example.com"
                aria-describedby={errors.email ? 'invite-email-error' : undefined}
              />
              {errors.email && (
                <p id="invite-email-error" className="text-sm text-destructive">
                  {errors.email}
                </p>
              )}
            </div>
            <div className="space-y-1">
              <label htmlFor="invite-role" className="text-sm font-medium">
                Role
              </label>
              <select
                id="invite-role"
                value={role}
                onChange={(e) => setRole(Number(e.target.value) as Role)}
                className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
              >
                {grantableRoles.map((r) => (
                  <option key={r} value={r}>
                    {ROLE_LABELS[r]}
                  </option>
                ))}
              </select>
            </div>
            {inviteUser.error && (
              <p className="text-sm text-destructive">
                Failed to send invitation. Please try again.
              </p>
            )}
          </div>
        </form>
        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" form="invite-user-form" disabled={inviteUser.isPending}>
            {inviteUser.isPending ? 'Sending...' : 'Send Invitation'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
