import * as React from 'react'
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

      <ResendEmailDialog
        open={resendOpen}
        onOpenChange={setResendOpen}
        invoiceId={invoiceId}
        onSuccess={onActionSuccess}
      />
      <MarkPaidDialog
        open={markPaidOpen}
        onOpenChange={setMarkPaidOpen}
        invoiceId={invoiceId}
        onSuccess={onActionSuccess}
      />
      <VoidInvoiceDialog
        open={voidOpen}
        onOpenChange={setVoidOpen}
        invoiceId={invoiceId}
        onSuccess={onActionSuccess}
      />
    </>
  )
}

interface ActionDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  invoiceId: string
  onSuccess?: () => void
}

function ResendEmailDialog({ open, onOpenChange, invoiceId, onSuccess }: ActionDialogProps) {
  const resend = useResendInvoiceEmail()
  const [error, setError] = React.useState<string | undefined>()

  React.useEffect(() => {
    if (!open && !resend.isPending) {
      setError(undefined)
      resend.reset()
    }
  }, [open, resend.isPending]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleConfirm() {
    try {
      await resend.mutateAsync(invoiceId)
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
          <DialogTitle>Resend Invoice Email</DialogTitle>
          <DialogDescription>
            Resend the invoice email for this invoice. The recipient will receive a new copy.
          </DialogDescription>
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
          <Button onClick={() => void handleConfirm()} disabled={resend.isPending}>
            {resend.isPending ? 'Sending...' : 'Resend Email'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MarkPaidDialog({ open, onOpenChange, invoiceId, onSuccess }: ActionDialogProps) {
  const markPaid = useMarkInvoicePaid()
  const [error, setError] = React.useState<string | undefined>()

  React.useEffect(() => {
    if (!open && !markPaid.isPending) {
      setError(undefined)
      markPaid.reset()
    }
  }, [open, markPaid.isPending]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleConfirm() {
    try {
      await markPaid.mutateAsync(invoiceId)
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
          <DialogTitle>Mark Invoice as Paid</DialogTitle>
          <DialogDescription>
            Confirm that this invoice has been paid. This will update the invoice status to PAID.
          </DialogDescription>
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
          <Button onClick={() => void handleConfirm()} disabled={markPaid.isPending}>
            {markPaid.isPending ? 'Updating...' : 'Mark as Paid'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function VoidInvoiceDialog({ open, onOpenChange, invoiceId, onSuccess }: ActionDialogProps) {
  const voidInvoice = useVoidInvoice()
  const [error, setError] = React.useState<string | undefined>()

  React.useEffect(() => {
    if (!open && !voidInvoice.isPending) {
      setError(undefined)
      voidInvoice.reset()
    }
  }, [open, voidInvoice.isPending]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleConfirm() {
    try {
      await voidInvoice.mutateAsync(invoiceId)
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
          <DialogTitle>Void Invoice</DialogTitle>
          <DialogDescription>
            Void this invoice and cancel any pending email deliveries. This action cannot be undone.
          </DialogDescription>
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
            variant="destructive"
            onClick={() => void handleConfirm()}
            disabled={voidInvoice.isPending}
          >
            {voidInvoice.isPending ? 'Voiding...' : 'Void Invoice'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
