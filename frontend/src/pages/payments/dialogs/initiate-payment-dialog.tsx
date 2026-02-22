import * as React from 'react'
import { Code } from '@connectrpc/connect'
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
import { useInitiatePayment } from './payment-mutations'

// IBAN pattern: 2 letter country code + 2 digits + up to 30 alphanumeric chars (no spaces)
const IBAN_PATTERN = /^[A-Z]{2}[0-9]{2}[A-Z0-9]{1,30}$/

interface FormData {
  debtorAccountId: string
  creditorIban: string
  amount: string
  currency: string
}

interface FormErrors {
  debtorAccountId?: string
  creditorIban?: string
  amount?: string
  currency?: string
  general?: string
}

export interface InitiatePaymentDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: (paymentOrderId: string) => void
}

export function InitiatePaymentDialog({
  open,
  onOpenChange,
  onSuccess,
}: InitiatePaymentDialogProps) {
  const initiate = useInitiatePayment()
  const [formData, setFormData] = React.useState<FormData>({
    debtorAccountId: '',
    creditorIban: '',
    amount: '',
    currency: 'GBP',
  })
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData({ debtorAccountId: '', creditorIban: '', amount: '', currency: 'GBP' })
      setErrors({})
      initiate.reset()
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  function validate(): boolean {
    const newErrors: FormErrors = {}

    if (!formData.debtorAccountId.trim()) {
      newErrors.debtorAccountId = 'Debtor account is required'
    }

    const normalizedIban = formData.creditorIban.trim().replace(/\s+/g, '')
    if (!normalizedIban) {
      newErrors.creditorIban = 'IBAN is required'
    } else if (!IBAN_PATTERN.test(normalizedIban)) {
      newErrors.creditorIban = 'Invalid IBAN format'
    }

    if (!formData.amount.trim()) {
      newErrors.amount = 'Amount is required'
    } else {
      const parsed = parseFloat(formData.amount)
      if (isNaN(parsed) || parsed <= 0) {
        newErrors.amount = 'Amount must be positive'
      }
    }

    setErrors(newErrors)
    return Object.keys(newErrors).length === 0
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return

    try {
      const normalizedIban = formData.creditorIban.trim().replace(/\s+/g, '')
      const result = await initiate.mutateAsync({
        debtorAccountId: formData.debtorAccountId.trim(),
        creditorReference: normalizedIban,
        amount: formData.amount.trim(),
        currency: formData.currency,
      })

      const paymentOrderId =
        (result as { paymentOrderId?: string } | null | undefined)?.paymentOrderId ?? ''
      onSuccess(paymentOrderId)
      onOpenChange(false)
    } catch (err) {
      const result = handleConnectError(err)

      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'creditor_reference') fieldMap.creditorIban = msg
          else if (field === 'debtor_account_id') fieldMap.debtorAccountId = msg
          else if (field === 'amount') fieldMap.amount = msg
          else fieldMap.general = msg
        }
        setErrors(fieldMap)
      } else {
        setErrors({ general: result.message })
      }
    }
  }

  function handleChange(field: keyof FormData) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Initiate Payment</DialogTitle>
          <DialogDescription>
            Create a new payment order. The payment will be processed through the saga workflow.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="initiate-payment-form">
          <div className="space-y-4 py-2">
            {errors.general && (
              <div
                role="alert"
                className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {errors.general}
              </div>
            )}

            <div className="space-y-1">
              <label htmlFor="debtorAccountId" className="text-sm font-medium">
                Debtor Account
              </label>
              <Input
                id="debtorAccountId"
                value={formData.debtorAccountId}
                onChange={handleChange('debtorAccountId')}
                placeholder="acct-001"
                aria-describedby={errors.debtorAccountId ? 'debtorAccountId-error' : undefined}
              />
              {errors.debtorAccountId && (
                <p id="debtorAccountId-error" className="text-sm text-destructive">
                  {errors.debtorAccountId}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="creditorIban" className="text-sm font-medium">
                Creditor IBAN
              </label>
              <Input
                id="creditorIban"
                value={formData.creditorIban}
                onChange={handleChange('creditorIban')}
                placeholder="GB29NWBK60161331926819"
                aria-describedby={errors.creditorIban ? 'creditorIban-error' : undefined}
              />
              {errors.creditorIban && (
                <p id="creditorIban-error" className="text-sm text-destructive">
                  {errors.creditorIban}
                </p>
              )}
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <label htmlFor="amount" className="text-sm font-medium">
                  Amount
                </label>
                <Input
                  id="amount"
                  value={formData.amount}
                  onChange={handleChange('amount')}
                  placeholder="100.00"
                  inputMode="decimal"
                  aria-describedby={errors.amount ? 'amount-error' : undefined}
                />
                {errors.amount && (
                  <p id="amount-error" className="text-sm text-destructive">
                    {errors.amount}
                  </p>
                )}
              </div>

              <div className="space-y-1">
                <label htmlFor="currency" className="text-sm font-medium">
                  Currency
                </label>
                <select
                  id="currency"
                  value={formData.currency}
                  onChange={handleChange('currency')}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                >
                  <option value="GBP">GBP</option>
                  <option value="USD">USD</option>
                  <option value="EUR">EUR</option>
                </select>
              </div>
            </div>
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="initiate-payment-form"
            disabled={initiate.isPending}
          >
            {initiate.isPending ? 'Initiating...' : 'Initiate Payment'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
