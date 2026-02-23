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

const CURRENCIES = ['GBP', 'USD', 'EUR']

// IBAN pattern: 2 letter country code + 2 digits + up to 30 alphanumeric chars
const IBAN_PATTERN = /^[A-Z]{2}[0-9]{2}[A-Z0-9]{1,30}$/

interface FormData {
  iban: string
  currency: string
  partyId: string
}

interface FormErrors {
  iban?: string
  partyId?: string
  general?: string
}

interface CreateAccountDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (accountId: string) => void
}

async function createAccount(
  tenantSlug: string,
  iban: string,
  currency: string,
  partyId: string,
): Promise<string> {
  const response = await fetch(
    `/api/meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-Slug': tenantSlug,
      },
      body: JSON.stringify({
        accountIdentification: iban,
        baseCurrency: currency,
        partyId,
      }),
    },
  )

  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as { message?: string }
    throw new Error(data.message ?? `Failed to create account: ${response.status}`)
  }

  const data = (await response.json()) as { accountId?: string }
  if (!data.accountId) {
    throw new Error('Account ID missing from response')
  }
  return data.accountId
}

export function CreateAccountDialog({ open, onOpenChange, onCreated }: CreateAccountDialogProps) {
  const { tenantSlug } = useTenantContext()
  const queryClient = useQueryClient()
  const [formData, setFormData] = React.useState<FormData>({
    iban: '',
    currency: 'GBP',
    partyId: '',
  })
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData({ iban: '', currency: 'GBP', partyId: '' })
      setErrors({})
    }
  }, [open])

  function validate(): FormErrors {
    const next: FormErrors = {}
    if (!tenantSlug) {
      next.general = 'No tenant selected'
      return next
    }
    const iban = formData.iban.trim().toUpperCase()
    if (!iban) {
      next.iban = 'IBAN is required'
    } else if (!IBAN_PATTERN.test(iban)) {
      next.iban = 'Enter a valid IBAN (e.g. GB82WEST12345698765432)'
    }
    if (!formData.partyId.trim()) {
      next.partyId = 'Party ID is required'
    }
    return next
  }

  const mutation = useMutation({
    mutationFn: () =>
      createAccount(
        tenantSlug ?? '',
        formData.iban.trim().toUpperCase(),
        formData.currency,
        formData.partyId.trim(),
      ),
    onSuccess: (accountId) => {
      queryClient.invalidateQueries({
        queryKey: tenantKeys.accounts(tenantSlug ?? ''),
      })
      onOpenChange(false)
      onCreated?.(accountId)
    },
    onError: (error: Error) => {
      setErrors((prev) => ({ ...prev, general: error.message }))
    },
  })

  function handleChange(field: keyof FormData) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field as keyof FormErrors]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const next = validate()
    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }
    setErrors({})
    mutation.mutate()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Account</DialogTitle>
          <DialogDescription>
            Open a new current account for a party.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-account-form">
          <div className="space-y-4 py-2">
            <div className="space-y-1">
              <label htmlFor="account-iban" className="text-sm font-medium">
                IBAN
              </label>
              <Input
                id="account-iban"
                value={formData.iban}
                onChange={handleChange('iban')}
                placeholder="GB82WEST12345698765432"
                aria-label="IBAN"
                aria-describedby={errors.iban ? 'account-iban-error' : undefined}
              />
              {errors.iban && (
                <p id="account-iban-error" className="text-sm text-destructive">
                  {errors.iban}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="account-currency" className="text-sm font-medium">
                Currency
              </label>
              <select
                id="account-currency"
                value={formData.currency}
                onChange={handleChange('currency')}
                aria-label="Currency"
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
              >
                {CURRENCIES.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
            </div>

            <div className="space-y-1">
              <label htmlFor="account-party-id" className="text-sm font-medium">
                Party ID
              </label>
              <Input
                id="account-party-id"
                value={formData.partyId}
                onChange={handleChange('partyId')}
                placeholder="party-001"
                aria-label="Party ID"
                aria-describedby={errors.partyId ? 'account-party-id-error' : undefined}
              />
              {errors.partyId && (
                <p id="account-party-id-error" className="text-sm text-destructive">
                  {errors.partyId}
                </p>
              )}
            </div>

            {errors.general && (
              <div role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {errors.general}
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
            form="create-account-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Creating...' : 'Create Account'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
