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
import { Input } from '@/components/ui/input'

const DISPUTE_REASON_OPTIONS = [
  { label: 'Amount Mismatch', value: 'DISPUTE_REASON_AMOUNT_MISMATCH' },
  { label: 'Missing Entry', value: 'DISPUTE_REASON_MISSING_ENTRY' },
  { label: 'Duplicate', value: 'DISPUTE_REASON_DUPLICATE' },
  { label: 'Timing', value: 'DISPUTE_REASON_TIMING' },
  { label: 'Other', value: 'DISPUTE_REASON_OTHER' },
]

type DisputeReason = (typeof DISPUTE_REASON_OPTIONS)[number]['value']

export interface CreateDisputeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  runId: string
  lineItem: {
    varianceId: string
    amount: string
    expectedAmount?: string
    timestamp?: string
  }
}

interface FormData {
  reason: DisputeReason | ''
  description: string
  expectedAmount: string
}

interface FormErrors {
  reason?: string
  description?: string
  expectedAmount?: string
  general?: string
}

function validateForm(data: FormData): FormErrors {
  const errors: FormErrors = {}

  if (!data.reason) {
    errors.reason = 'Reason is required'
  }

  if (!data.description.trim()) {
    errors.description = 'Description is required'
  } else if (data.description.length > 1000) {
    errors.description = 'Description must be 1000 characters or fewer'
  }

  if (data.reason === 'DISPUTE_REASON_AMOUNT_MISMATCH' && !data.expectedAmount.trim()) {
    errors.expectedAmount = 'Expected amount is required for amount mismatch disputes'
  }

  return errors
}

async function createDispute(
  runId: string,
  varianceId: string,
  data: FormData,
): Promise<{ disputeId: string }> {
  const body: Record<string, unknown> = {
    varianceId,
    reason: data.reason,
    description: data.description,
  }

  if (data.reason === 'DISPUTE_REASON_AMOUNT_MISMATCH' && data.expectedAmount) {
    body.expectedAmount = data.expectedAmount
  }

  const response = await fetch(`/api/v1/reconciliation/runs/${runId}/disputes`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    const err = (await response.json().catch(() => ({}))) as { message?: string }
    throw new Error(err.message ?? `Failed to create dispute: ${response.status}`)
  }

  return response.json() as Promise<{ disputeId: string }>
}

export function CreateDisputeDialog({
  open,
  onOpenChange,
  runId,
  lineItem,
}: CreateDisputeDialogProps) {
  const queryClient = useQueryClient()

  const [formData, setFormData] = React.useState<FormData>({
    reason: '',
    description: '',
    expectedAmount: '',
  })
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData({ reason: '', description: '', expectedAmount: '' })
      setErrors({})
    }
  }, [open])

  const mutation = useMutation({
    mutationFn: (vars: { runId: string; varianceId: string; data: FormData }) =>
      createDispute(vars.runId, vars.varianceId, vars.data),
    onSuccess: (_result, vars) => {
      void queryClient.invalidateQueries({ queryKey: ['reconciliation-disputes', vars.runId] })
      if (lineItem.varianceId === vars.varianceId) {
        onOpenChange(false)
      }
    },
    onError: (err: Error) => {
      setErrors({ general: err.message })
    },
  })

  function handleChange<K extends keyof FormData>(field: K, value: FormData[K]) {
    setFormData((prev) => ({ ...prev, [field]: value }))
    setErrors((prev) => {
      if (!prev[field as keyof FormErrors] && !prev.general) return prev
      return { ...prev, [field]: undefined, general: undefined }
    })
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const next = validateForm(formData)
    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }
    setErrors({})
    mutation.mutate({ runId, varianceId: lineItem.varianceId, data: { ...formData } })
  }

  const showExpectedAmount = formData.reason === 'DISPUTE_REASON_AMOUNT_MISMATCH'

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Raise Dispute</DialogTitle>
          <DialogDescription>
            Raise a dispute on this variance item. Provide the reason and a description.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-dispute-form">
          <div className="space-y-4 py-2">
            {errors.general && (
              <div
                role="alert"
                className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {errors.general}
              </div>
            )}

            {/* Read-only line item context */}
            <div className="space-y-1">
              <span className="text-sm font-medium">Disputed Item</span>
              <div
                aria-label="Disputed item details"
                className="rounded-md border bg-muted/50 px-3 py-2 text-sm space-y-1"
              >
                <div>
                  <span className="text-muted-foreground">Variance ID: </span>
                  <span className="font-mono">{lineItem.varianceId}</span>
                </div>
                <div>
                  <span className="text-muted-foreground">Amount: </span>
                  <span>{lineItem.amount}</span>
                </div>
                {lineItem.expectedAmount && (
                  <div>
                    <span className="text-muted-foreground">Expected: </span>
                    <span>{lineItem.expectedAmount}</span>
                  </div>
                )}
                {lineItem.timestamp && (
                  <div>
                    <span className="text-muted-foreground">Timestamp: </span>
                    <span>{lineItem.timestamp}</span>
                  </div>
                )}
              </div>
            </div>

            {/* Reason */}
            <div className="space-y-1">
              <label htmlFor="dispute-reason" className="text-sm font-medium">
                Reason
              </label>
              <select
                id="dispute-reason"
                value={formData.reason}
                onChange={(e) => handleChange('reason', e.target.value as DisputeReason)}
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                aria-label="Reason"
                aria-describedby={errors.reason ? 'dispute-reason-error' : undefined}
              >
                <option value="">Select a reason</option>
                {DISPUTE_REASON_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
              {errors.reason && (
                <p id="dispute-reason-error" className="text-sm text-destructive">
                  {errors.reason}
                </p>
              )}
            </div>

            {/* Description */}
            <div className="space-y-1">
              <label htmlFor="dispute-description" className="text-sm font-medium">
                Description
              </label>
              <textarea
                id="dispute-description"
                value={formData.description}
                onChange={(e) => handleChange('description', e.target.value)}
                placeholder="Describe the issue in detail..."
                maxLength={1000}
                rows={4}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs resize-none focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                aria-describedby={errors.description ? 'dispute-description-error' : undefined}
              />
              {errors.description && (
                <p id="dispute-description-error" className="text-sm text-destructive">
                  {errors.description}
                </p>
              )}
            </div>

            {/* Conditional expected amount */}
            {showExpectedAmount && (
              <div className="space-y-1">
                <label htmlFor="dispute-expected-amount" className="text-sm font-medium">
                  Expected Amount
                </label>
                <Input
                  id="dispute-expected-amount"
                  type="number"
                  step="any"
                  value={formData.expectedAmount}
                  onChange={(e) => handleChange('expectedAmount', e.target.value)}
                  placeholder="Enter expected amount"
                  aria-describedby={
                    errors.expectedAmount ? 'dispute-expected-amount-error' : undefined
                  }
                />
                {errors.expectedAmount && (
                  <p id="dispute-expected-amount-error" className="text-sm text-destructive">
                    {errors.expectedAmount}
                  </p>
                )}
              </div>
            )}
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="create-dispute-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Submitting...' : 'Raise Dispute'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
