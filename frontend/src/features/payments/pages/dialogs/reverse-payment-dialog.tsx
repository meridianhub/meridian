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
import { useReversePayment } from './payment-mutations'

interface ReversePaymentDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
  paymentOrderId: string
  currentStatus: string
}

export function ReversePaymentDialog({
  open,
  onOpenChange,
  onSuccess,
  paymentOrderId,
  currentStatus,
}: ReversePaymentDialogProps) {
  const reverse = useReversePayment()
  const [reason, setReason] = React.useState('')
  const [reasonError, setReasonError] = React.useState<string | undefined>()
  const [generalError, setGeneralError] = React.useState<string | undefined>()

  const canReverse = currentStatus === 'COMPLETED'

  React.useEffect(() => {
    if (!open) {
      setReason('')
      setReasonError(undefined)
      setGeneralError(undefined)
      reverse.reset()
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()

    if (!reason.trim()) {
      setReasonError('Reason is required')
      return
    }

    try {
      await reverse.mutateAsync({
        paymentOrderId,
        reversalReason: reason.trim(),
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
          <DialogTitle>Reverse Payment</DialogTitle>
          <DialogDescription>
            Reverse payment order <span className="font-mono font-medium">{paymentOrderId}</span>.
            This will create compensating ledger entries and transition the order to REVERSED.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="reverse-payment-form">
          <div className="space-y-4 py-2">
            {!canReverse && (
              <div
                role="alert"
                className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800"
              >
                This payment can only be reversed when completed. Current status: {currentStatus}
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
              <label htmlFor="reversalReason" className="text-sm font-medium">
                Reversal Reason
              </label>
              <Input
                id="reversalReason"
                value={reason}
                onChange={(e) => {
                  setReason(e.target.value)
                  if (reasonError) setReasonError(undefined)
                }}
                placeholder="Reason for reversal"
                disabled={!canReverse}
                aria-describedby={reasonError ? 'reversalReason-error' : undefined}
              />
              {reasonError && (
                <p id="reversalReason-error" className="text-sm text-destructive">
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
            form="reverse-payment-form"
            variant="destructive"
            disabled={!canReverse || reverse.isPending}
          >
            {reverse.isPending ? 'Reversing...' : 'Confirm Reversal'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
