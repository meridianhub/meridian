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
import { useAuthenticatedFetch } from '@/hooks/use-authenticated-fetch'
import { amountToBigInt } from './account-form-utils'

interface DepositDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  accountId: string
  currency: string
}

function validateAmount(value: string): string | null {
  const trimmed = value.trim()
  if (!trimmed) return 'Amount is required'
  try {
    const minorUnits = amountToBigInt(trimmed)
    if (minorUnits <= 0n) return 'Amount must be greater than zero'
  } catch (err) {
    // amountToBigInt throws 'Amount must be positive' for negative values
    const msg = err instanceof Error ? err.message : 'Invalid amount'
    if (msg === 'Amount must be positive') return 'Amount must be greater than zero'
    return 'Invalid amount'
  }
  return null
}

export function DepositDialog({ open, onOpenChange, accountId, currency }: DepositDialogProps) {
  const { tenantSlug } = useTenantContext()
  const authFetch = useAuthenticatedFetch()
  const queryClient = useQueryClient()
  const [amount, setAmount] = React.useState('')
  const [amountError, setAmountError] = React.useState<string | null>(null)
  const [serverError, setServerError] = React.useState<string | null>(null)

  React.useEffect(() => {
    if (!open) {
      setAmount('')
      setAmountError(null)
      setServerError(null)
    }
  }, [open])

  const mutation = useMutation({
    mutationFn: async () => {
      const minorUnits = amountToBigInt(amount).toString()
      const response = await authFetch(
        `/meridian.current_account.v1.CurrentAccountService/DepositFunds`,
        {
          method: 'POST',
          body: JSON.stringify({ accountId, amount: { amount: minorUnits } }),
        },
      )
      if (!response.ok) {
        const data = (await response.json().catch(() => ({}))) as { message?: string }
        throw new Error(data.message ?? `Failed to deposit: ${response.status}`)
      }
    },
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

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const error = validateAmount(amount)
    if (error) {
      setAmountError(error)
      return
    }
    setAmountError(null)
    setServerError(null)
    mutation.mutate()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Deposit Funds</DialogTitle>
          <DialogDescription>
            Deposit funds into account {accountId} ({currency})
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="deposit-form">
          <div className="space-y-4 py-2">
            <div className="space-y-1">
              <label htmlFor="deposit-amount" className="text-sm font-medium">
                Amount ({currency})
              </label>
              <Input
                id="deposit-amount"
                value={amount}
                onChange={(e) => {
                  setAmount(e.target.value)
                  if (amountError) setAmountError(null)
                }}
                placeholder="0.00"
                type="text"
                inputMode="decimal"
                aria-describedby={amountError ? 'deposit-amount-error' : undefined}
                aria-label="Amount"
              />
              {amountError && (
                <p id="deposit-amount-error" className="text-sm text-destructive">
                  {amountError}
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
            form="deposit-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Depositing...' : 'Deposit'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
