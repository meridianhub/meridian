import * as React from 'react'
import type { UseMutationResult } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { handleConnectError } from '@/lib/error-handling'
import { useResendInvoiceEmail, useMarkInvoicePaid, useVoidInvoice } from '../api/hooks'
import type { Invoice } from '../api/types'

const ACTIONABLE_STATUSES = new Set<Invoice['status']>(['ISSUED', 'OVERDUE'])

interface InvoiceActionsProps {
  invoiceId: string
  status: Invoice['status']
  onActionSuccess?: () => void
}

export function InvoiceActions({ invoiceId, status, onActionSuccess }: InvoiceActionsProps) {
  const [resendOpen, setResendOpen] = React.useState(false)
  const [markPaidOpen, setMarkPaidOpen] = React.useState(false)
  const [voidOpen, setVoidOpen] = React.useState(false)

  const resend = useResendInvoiceEmail()
  const markPaid = useMarkInvoicePaid()
  const voidInvoice = useVoidInvoice()

  const showActionableButtons = ACTIONABLE_STATUSES.has(status)

  return (
    <>
      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={() => setResendOpen(true)}>
          Resend Email
        </Button>
        {showActionableButtons && (
          <>
            <Button variant="outline" size="sm" onClick={() => setMarkPaidOpen(true)}>
              Mark as Paid
            </Button>
            <Button variant="destructive" size="sm" onClick={() => setVoidOpen(true)}>
              Void Invoice
            </Button>
          </>
        )}
      </div>

      <ActionDialog
        open={resendOpen}
        onOpenChange={setResendOpen}
        invoiceId={invoiceId}
        onSuccess={onActionSuccess}
        mutation={resend}
        title="Resend Invoice Email"
        description="Resend the invoice email for this invoice. The recipient will receive a new copy."
        confirmLabel="Resend Email"
        pendingLabel="Sending..."
      />
      <ActionDialog
        open={markPaidOpen}
        onOpenChange={setMarkPaidOpen}
        invoiceId={invoiceId}
        onSuccess={onActionSuccess}
        mutation={markPaid}
        title="Mark Invoice as Paid"
        description="Confirm that this invoice has been paid. This will update the invoice status to PAID."
        confirmLabel="Mark as Paid"
        pendingLabel="Updating..."
      />
      <ActionDialog
        open={voidOpen}
        onOpenChange={setVoidOpen}
        invoiceId={invoiceId}
        onSuccess={onActionSuccess}
        mutation={voidInvoice}
        title="Void Invoice"
        description="Void this invoice and cancel any pending email deliveries. This action cannot be undone."
        confirmLabel="Void Invoice"
        pendingLabel="Voiding..."
        confirmVariant="destructive"
      />
    </>
  )
}

interface ActionDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  invoiceId: string
  onSuccess?: () => void
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  mutation: UseMutationResult<any, Error, string>
  title: string
  description: string
  confirmLabel: string
  pendingLabel: string
  confirmVariant?: 'default' | 'destructive' | 'outline'
}

function ActionDialog({
  open,
  onOpenChange,
  invoiceId,
  onSuccess,
  mutation,
  title,
  description,
  confirmLabel,
  pendingLabel,
  confirmVariant = 'default',
}: ActionDialogProps) {
  const [error, setError] = React.useState<string | undefined>()

  React.useEffect(() => {
    if (!open && !mutation.isPending) {
      setError(undefined)
      mutation.reset()
    }
  }, [open, mutation.isPending]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleConfirm() {
    try {
      await mutation.mutateAsync(invoiceId)
      onSuccess?.()
      onOpenChange(false)
    } catch (err) {
      const result = handleConnectError(err)
      setError(result.message)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        {error && (
          <div
            role="alert"
            className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant={confirmVariant}
            onClick={() => void handleConfirm()}
            disabled={mutation.isPending}
          >
            {mutation.isPending ? pendingLabel : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
