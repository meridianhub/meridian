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
import { Input } from '@/components/ui/input'
import { handleConnectError } from '@/lib/error-handling'
import { useCancelPayment } from './payment-mutations'

const CANCELLABLE_STATUSES = new Set(['INITIATED', 'RESERVED'])

interface CancelPaymentDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
  paymentOrderId: string
  currentStatus: string
}

export function CancelPaymentDialog({
  open,
  onOpenChange,
  onSuccess,
  paymentOrderId,
  currentStatus,
}: CancelPaymentDialogProps) {
  const cancel = useCancelPayment()
  const [reason, setReason] = React.useState('')
  const [reasonError, setReasonError] = React.useState<string | undefined>()
  const [generalError, setGeneralError] = React.useState<string | undefined>()

  const canCancel = CANCELLABLE_STATUSES.has(currentStatus)

  React.useEffect(() => {
    if (!open) {
      setReason('')
      setReasonError(undefined)
      setGeneralError(undefined)
      cancel.reset()
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()

    if (!reason.trim()) {
      setReasonError('Reason is required')
      return
    }

    try {
      await cancel.mutateAsync({
        paymentOrderId,
        cancellationReason: reason.trim(),
      })
      onSuccess()
      onOpenChange(false)
    } catch (err) {
      const result = handleConnectError(err)
      setGeneralError(result.message)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Cancel Payment</DialogTitle>
          <DialogDescription>
            Cancel payment order <span className="font-mono font-medium">{paymentOrderId}</span>.
            This action cannot be undone.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="cancel-payment-form">
          <div className="space-y-4 py-2">
            {!canCancel && (
              <div
                role="alert"
                className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800"
              >
                This payment cannot be cancelled while executing. It must wait for the gateway
                response.
              </div>
            )}

            {generalError && (
              <div
                role="alert"
                className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {generalError}
              </div>
            )}

            <div className="space-y-1">
              <label htmlFor="cancellationReason" className="text-sm font-medium">
                Cancellation Reason
              </label>
              <Input
                id="cancellationReason"
                value={reason}
                onChange={(e) => {
                  setReason(e.target.value)
                  if (reasonError) setReasonError(undefined)
                }}
                placeholder="Reason for cancellation"
                disabled={!canCancel}
                aria-describedby={reasonError ? 'cancellationReason-error' : undefined}
              />
              {reasonError && (
                <p id="cancellationReason-error" className="text-sm text-destructive">
                  {reasonError}
                </p>
              )}
            </div>
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="cancel-payment-form"
            variant="destructive"
            disabled={!canCancel || cancel.isPending}
          >
            {cancel.isPending ? 'Cancelling...' : 'Confirm Cancellation'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
