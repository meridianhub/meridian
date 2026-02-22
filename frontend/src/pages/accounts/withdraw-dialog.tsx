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

interface WithdrawDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  accountId: string
  currency: string
}

type Step = 'initiate' | 'confirm'

function validateAmount(value: string): string | null {
  if (!value.trim()) return 'Amount is required'
  if (isNaN(parseFloat(value))) return 'Invalid amount'
  if (parseFloat(value) <= 0) return 'Amount must be greater than zero'
  return null
}

async function initiateWithdrawal(
  tenantSlug: string,
  accountId: string,
  amountMinorUnits: string,
): Promise<string> {
  const response = await fetch(
    `/api/meridian.current_account.v1.CurrentAccountService/InitiateWithdrawal`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-Slug': tenantSlug,
      },
      body: JSON.stringify({
        accountId,
        amount: { amount: amountMinorUnits },
      }),
    },
  )

  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as { message?: string }
    throw new Error(data.message ?? `Failed to initiate withdrawal: ${response.status}`)
  }

  const data = (await response.json()) as { withdrawalId: string }
  return data.withdrawalId
}

async function executeWithdrawal(tenantSlug: string, withdrawalId: string): Promise<void> {
  const response = await fetch(
    `/api/meridian.current_account.v1.CurrentAccountService/ExecuteWithdrawal`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-Slug': tenantSlug,
      },
      body: JSON.stringify({ withdrawalId }),
    },
  )

  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as { message?: string }
    throw new Error(data.message ?? `Failed to execute withdrawal: ${response.status}`)
  }
}

export function WithdrawDialog({ open, onOpenChange, accountId, currency }: WithdrawDialogProps) {
  const { tenantSlug } = useTenantContext()
  const queryClient = useQueryClient()
  const [step, setStep] = React.useState<Step>('initiate')
  const [amount, setAmount] = React.useState('')
  const [amountError, setAmountError] = React.useState<string | null>(null)
  const [serverError, setServerError] = React.useState<string | null>(null)
  const [withdrawalId, setWithdrawalId] = React.useState<string | null>(null)

  React.useEffect(() => {
    if (!open) {
      setStep('initiate')
      setAmount('')
      setAmountError(null)
      setServerError(null)
      setWithdrawalId(null)
    }
  }, [open])

  const initiateMutation = useMutation({
    mutationFn: () => {
      const minorUnits = amountToBigInt(amount).toString()
      return initiateWithdrawal(tenantSlug ?? '', accountId, minorUnits)
    },
    onSuccess: (id) => {
      setWithdrawalId(id)
      setStep('confirm')
    },
    onError: (error: Error) => {
      setServerError(error.message)
    },
  })

  const executeMutation = useMutation({
    mutationFn: () => {
      return executeWithdrawal(tenantSlug ?? '', withdrawalId ?? '')
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

  function handleInitiate(e: React.FormEvent) {
    e.preventDefault()
    const error = validateAmount(amount)
    if (error) {
      setAmountError(error)
      return
    }
    setAmountError(null)
    setServerError(null)
    initiateMutation.mutate()
  }

  function handleExecute(e: React.FormEvent) {
    e.preventDefault()
    setServerError(null)
    executeMutation.mutate()
  }

  const isPending = initiateMutation.isPending || executeMutation.isPending

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Withdraw Funds</DialogTitle>
          <DialogDescription>
            {step === 'initiate'
              ? `Initiate a withdrawal from account ${accountId} (${currency})`
              : `Confirm withdrawal of ${currency} ${amount} from account ${accountId}`}
          </DialogDescription>
        </DialogHeader>

        {step === 'initiate' ? (
          <form onSubmit={(e) => void handleInitiate(e)} id="withdraw-form">
            <div className="space-y-4 py-2">
              <div className="space-y-1">
                <label htmlFor="withdraw-amount" className="text-sm font-medium">
                  Amount ({currency})
                </label>
                <Input
                  id="withdraw-amount"
                  value={amount}
                  onChange={(e) => {
                    setAmount(e.target.value)
                    if (amountError) setAmountError(null)
                  }}
                  placeholder="0.00"
                  type="text"
                  inputMode="decimal"
                  aria-describedby={amountError ? 'withdraw-amount-error' : undefined}
                  aria-label="Amount"
                />
                {amountError && (
                  <p id="withdraw-amount-error" className="text-sm text-destructive">
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
        ) : (
          <form onSubmit={(e) => void handleExecute(e)} id="withdraw-form">
            <div className="space-y-4 py-2">
              <p className="text-sm text-muted-foreground">
                Please confirm the withdrawal details below:
              </p>
              <dl className="grid grid-cols-2 gap-2 text-sm">
                <dt className="font-medium">Amount</dt>
                <dd>{currency} {amount}</dd>
                <dt className="font-medium">Account</dt>
                <dd>{accountId}</dd>
                <dt className="font-medium">Withdrawal ID</dt>
                <dd className="font-mono text-xs">{withdrawalId}</dd>
              </dl>

              {serverError && (
                <div role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                  {serverError}
                </div>
              )}
            </div>
          </form>
        )}

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="withdraw-form"
            disabled={isPending}
          >
            {step === 'initiate'
              ? initiateMutation.isPending ? 'Initiating...' : 'Initiate'
              : executeMutation.isPending ? 'Confirming...' : 'Confirm'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
