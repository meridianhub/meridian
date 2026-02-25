import * as React from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'

export type ControlAction = 'freeze' | 'unfreeze' | 'close'

interface ControlDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  accountId: string
  action: ControlAction
}

const ACTION_CONFIG: Record<
  ControlAction,
  {
    title: string
    description: (accountId: string) => string
    endpoint: string
    confirmLabel: string
    variant: 'default' | 'destructive'
  }
> = {
  freeze: {
    title: 'Freeze Account',
    description: (id) => `Are you sure you want to freeze account ${id}? No transactions will be permitted while frozen.`,
    endpoint: 'FreezeAccount',
    confirmLabel: 'Confirm Freeze',
    variant: 'default',
  },
  unfreeze: {
    title: 'Unfreeze Account',
    description: (id) => `Are you sure you want to unfreeze account ${id}? Transactions will resume normally.`,
    endpoint: 'UnfreezeAccount',
    confirmLabel: 'Confirm Unfreeze',
    variant: 'default',
  },
  close: {
    title: 'Close Account',
    description: (id) => `Are you sure you want to close account ${id}? This action cannot be undone.`,
    endpoint: 'CloseAccount',
    confirmLabel: 'Confirm Close',
    variant: 'destructive',
  },
}

async function performAccountControl(
  tenantSlug: string,
  accountId: string,
  endpoint: string,
): Promise<void> {
  const response = await fetch(
    `/meridian.current_account.v1.CurrentAccountService/${endpoint}`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-Slug': tenantSlug,
      },
      body: JSON.stringify({ accountId }),
    },
  )

  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as { message?: string }
    throw new Error(data.message ?? `Failed to ${endpoint}: ${response.status}`)
  }
}

export function ControlDialog({ open, onOpenChange, accountId, action }: ControlDialogProps) {
  const { tenantSlug } = useTenantContext()
  const queryClient = useQueryClient()
  const [serverError, setServerError] = React.useState<string | null>(null)

  const config = ACTION_CONFIG[action]

  React.useEffect(() => {
    if (!open) {
      setServerError(null)
    }
  }, [open])

  const mutation = useMutation({
    mutationFn: () =>
      performAccountControl(tenantSlug ?? '', accountId, config.endpoint),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: tenantKeys.account(tenantSlug ?? '', accountId),
      })
      onOpenChange(false)
    },
    onError: (error: Error) => {
      setServerError(error.message)
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{config.title}</DialogTitle>
          <DialogDescription>{config.description(accountId)}</DialogDescription>
        </DialogHeader>

        {serverError && (
          <div role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
            {serverError}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant={config.variant}
            type="button"
            disabled={mutation.isPending}
            onClick={() => {
              setServerError(null)
              mutation.mutate()
            }}
          >
            {mutation.isPending ? 'Processing...' : config.confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
