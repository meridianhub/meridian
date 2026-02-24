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
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { amountToBigInt } from './account-form-utils'

export interface CreateLienDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  accountId: string
  instrumentCode: string
  accountType: 'current' | 'internal'
  decimalPlaces?: number
}

function validateAmount(value: string, decimalPlaces: number): string | null {
  const trimmed = value.trim()
  if (!trimmed) return 'Amount is required'
  try {
    const minorUnits = amountToBigInt(trimmed, decimalPlaces)
    if (minorUnits <= 0n) return 'Amount must be greater than zero'
  } catch (err) {
    const msg = err instanceof Error ? err.message : 'Invalid amount'
    if (msg === 'Amount must be positive') return 'Amount must be greater than zero'
    return 'Invalid amount'
  }
  return null
}

function validateReason(value: string): string | null {
  const trimmed = value.trim()
  if (!trimmed) return 'Reason is required'
  if (trimmed.length > 255) return 'Reason must be 255 characters or fewer'
  return null
}

function validateExpiry(value: string): string | null {
  if (!value) return null
  const date = new Date(value)
  if (isNaN(date.getTime())) return 'Invalid date'
  if (date <= new Date()) return 'Expiry must be in the future'
  return null
}

async function initiateLien(
  tenantSlug: string,
  accountId: string,
  accountType: 'current' | 'internal',
  amountMinorUnits: string,
  reason: string,
  expiresAtSeconds?: string,
): Promise<string> {
  const serviceName =
    accountType === 'current'
      ? 'meridian.current_account.v1.CurrentAccountService'
      : 'meridian.internal_bank_account.v1.InternalBankAccountService'

  const body: Record<string, unknown> = {
    accountId,
    paymentOrderReference: reason,
  }

  if (accountType === 'current') {
    body.amount = { amount: amountMinorUnits }
  } else {
    body.input = { amount: amountMinorUnits }
  }

  if (expiresAtSeconds) {
    body.expiresAt = { seconds: expiresAtSeconds }
  }

  const response = await fetch(`/api/${serviceName}/InitiateLien`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Tenant-Slug': tenantSlug,
    },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as { message?: string }
    throw new Error(data.message ?? `Failed to create lien: ${response.status}`)
  }

  const data = (await response.json()) as { lien?: { lienId?: string } }
  const lienId = data.lien?.lienId ?? ''
  if (!lienId) {
    throw new Error('Lien ID missing from response')
  }
  return lienId
}

export function CreateLienDialog({
  open,
  onOpenChange,
  accountId,
  instrumentCode,
  accountType,
  decimalPlaces = 2,
}: CreateLienDialogProps) {
  const { tenantSlug } = useTenantContext()
  const queryClient = useQueryClient()

  const [amount, setAmount] = React.useState('')
  const [reason, setReason] = React.useState('')
  const [expiry, setExpiry] = React.useState('')
  const [amountError, setAmountError] = React.useState<string | null>(null)
  const [reasonError, setReasonError] = React.useState<string | null>(null)
  const [expiryError, setExpiryError] = React.useState<string | null>(null)
  const [serverError, setServerError] = React.useState<string | null>(null)
  const [successLienId, setSuccessLienId] = React.useState<string | null>(null)

  React.useEffect(() => {
    if (!open) {
      setAmount('')
      setReason('')
      setExpiry('')
      setAmountError(null)
      setReasonError(null)
      setExpiryError(null)
      setServerError(null)
      setSuccessLienId(null)
    }
  }, [open])

  const mutation = useMutation({
    mutationFn: () => {
      const minorUnits = amountToBigInt(amount, decimalPlaces).toString()
      const expiresAtSeconds = expiry
        ? Math.floor(new Date(expiry).getTime() / 1000).toString()
        : undefined
      return initiateLien(
        tenantSlug ?? '',
        accountId,
        accountType,
        minorUnits,
        reason.trim(),
        expiresAtSeconds,
      )
    },
    onSuccess: (lienId) => {
      queryClient.invalidateQueries({
        queryKey: tenantKeys.account(tenantSlug ?? '', accountId),
      })
      queryClient.invalidateQueries({
        queryKey: tenantKeys.liens(tenantSlug ?? '', accountId),
      })
      setSuccessLienId(lienId)
    },
    onError: (error: Error) => {
      setServerError(error.message)
    },
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()

    const amtErr = validateAmount(amount, decimalPlaces)
    const rsnErr = validateReason(reason)
    const expErr = validateExpiry(expiry)

    setAmountError(amtErr)
    setReasonError(rsnErr)
    setExpiryError(expErr)

    if (amtErr || rsnErr || expErr) return

    setServerError(null)
    mutation.mutate()
  }

  if (successLienId) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Lien Created</DialogTitle>
            <DialogDescription>
              The lien has been successfully created on account {accountId}.
            </DialogDescription>
          </DialogHeader>
          <div className="py-2">
            <p className="text-sm text-muted-foreground">Lien ID</p>
            <p className="font-mono text-sm" data-testid="lien-id">{successLienId}</p>
          </div>
          <DialogFooter>
            <Button onClick={() => onOpenChange(false)}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Lien</DialogTitle>
          <DialogDescription>
            Reserve funds on account {accountId} ({instrumentCode})
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-lien-form">
          <div className="space-y-4 py-2">
            <div className="space-y-1">
              <label htmlFor="lien-amount" className="text-sm font-medium">
                Amount ({instrumentCode})
              </label>
              <Input
                id="lien-amount"
                value={amount}
                onChange={(e) => {
                  setAmount(e.target.value)
                  if (amountError) setAmountError(null)
                }}
                placeholder="0.00"
                type="text"
                inputMode="decimal"
                aria-describedby={amountError ? 'lien-amount-error' : undefined}
                aria-label="Amount"
              />
              {amountError && (
                <p id="lien-amount-error" className="text-sm text-destructive">
                  {amountError}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="lien-reason" className="text-sm font-medium">
                Reason
              </label>
              <Input
                id="lien-reason"
                value={reason}
                onChange={(e) => {
                  setReason(e.target.value)
                  if (reasonError) setReasonError(null)
                }}
                placeholder="e.g. payment-order-123"
                aria-describedby={reasonError ? 'lien-reason-error' : undefined}
              />
              {reasonError && (
                <p id="lien-reason-error" className="text-sm text-destructive">
                  {reasonError}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="lien-expiry" className="text-sm font-medium">
                Expiry (optional)
              </label>
              <Input
                id="lien-expiry"
                value={expiry}
                onChange={(e) => {
                  setExpiry(e.target.value)
                  if (expiryError) setExpiryError(null)
                }}
                type="datetime-local"
                aria-describedby={expiryError ? 'lien-expiry-error' : undefined}
              />
              {expiryError && (
                <p id="lien-expiry-error" className="text-sm text-destructive">
                  {expiryError}
                </p>
              )}
            </div>

            {serverError && (
              <div role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {serverError}
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
            form="create-lien-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Creating...' : 'Create Lien'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
