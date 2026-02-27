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
import { useAuthenticatedFetch } from '@/hooks/use-authenticated-fetch'

type Scope = 'ALL_ACCOUNTS' | 'SELECTED_ACCOUNTS'

interface FormData {
  settlementDate: string
  scope: Scope
  accountId: string
  description: string
}

interface FormErrors {
  settlementDate?: string
  accountId?: string
  description?: string
  general?: string
}

export interface InitiateReconciliationDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: (runId: string) => void
}

function validateForm(data: FormData): FormErrors {
  const errors: FormErrors = {}

  if (!data.settlementDate) {
    errors.settlementDate = 'Settlement date is required'
  } else {
    // Compare as calendar dates using padded local date parts (avoids UTC/local timezone issues)
    const now = new Date()
    const y = now.getFullYear()
    const m = String(now.getMonth() + 1).padStart(2, '0')
    const d = String(now.getDate()).padStart(2, '0')
    const todayStr = `${y}-${m}-${d}`
    if (data.settlementDate > todayStr) {
      errors.settlementDate = 'Settlement date cannot be in the future'
    }
  }

  if (data.scope === 'SELECTED_ACCOUNTS' && !data.accountId.trim()) {
    errors.accountId = 'Account ID is required when scope is Selected Accounts'
  }

  if (data.description && (data.description.length < 1 || data.description.length > 255)) {
    errors.description = 'Description must be between 1 and 255 characters'
  }

  return errors
}

async function initiateReconciliation(
  data: FormData,
  idempotencyKey: string,
  fetchFn: typeof fetch = fetch,
): Promise<string> {
  const date = new Date(data.settlementDate)
  const periodStart = new Date(date)
  periodStart.setUTCHours(0, 0, 0, 0)
  const periodEnd = new Date(date)
  periodEnd.setUTCHours(23, 59, 59, 999)

  const accountId = data.scope === 'SELECTED_ACCOUNTS' ? data.accountId.trim() : 'system'

  const body: Record<string, unknown> = {
    accountId,
    scope:
      data.scope === 'ALL_ACCOUNTS'
        ? 'RECONCILIATION_SCOPE_FULL'
        : 'RECONCILIATION_SCOPE_ACCOUNT',
    settlementType: 'SETTLEMENT_TYPE_ON_DEMAND',
    periodStart: periodStart.toISOString(),
    periodEnd: periodEnd.toISOString(),
    initiatedBy: 'operations-console',
    idempotencyKey: { key: idempotencyKey },
  }

  if (data.description) {
    body.attributes = { description: data.description }
  }

  const response = await fetchFn(
    `/meridian.reconciliation.v1.AccountReconciliationService/InitiateAccountReconciliation`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    },
  )

  if (!response.ok) {
    const err = (await response.json().catch(() => ({}))) as { message?: string }
    throw new Error(err.message ?? `Failed to initiate reconciliation: ${response.status}`)
  }

  const result = (await response.json()) as { run?: { runId?: string } }
  return result.run?.runId ?? ''
}

export function InitiateReconciliationDialog({
  open,
  onOpenChange,
  onSuccess,
}: InitiateReconciliationDialogProps) {
  const authFetch = useAuthenticatedFetch()
  const queryClient = useQueryClient()

  const [formData, setFormData] = React.useState<FormData>({
    settlementDate: '',
    scope: 'ALL_ACCOUNTS',
    accountId: '',
    description: '',
  })
  const [errors, setErrors] = React.useState<FormErrors>({})

  // Generate a fresh idempotency key each time the dialog opens
  const idempotencyKey = React.useMemo(() => crypto.randomUUID(), [open]) // eslint-disable-line react-hooks/exhaustive-deps

  React.useEffect(() => {
    if (!open) {
      setFormData({ settlementDate: '', scope: 'ALL_ACCOUNTS', accountId: '', description: '' })
      setErrors({})
    }
  }, [open])

  const mutation = useMutation({
    mutationFn: () =>
      initiateReconciliation(formData, idempotencyKey, authFetch),
    onSuccess: (runId) => {
      void queryClient.invalidateQueries({ queryKey: ['reconciliation-runs'] })
      onSuccess(runId)
      onOpenChange(false)
    },
    onError: (err: Error) => {
      setErrors({ general: err.message })
    },
  })

  function handleChange<K extends keyof FormData>(field: K, value: FormData[K]) {
    setFormData((prev) => ({ ...prev, [field]: value }))
    if (errors[field as keyof FormErrors]) {
      setErrors((prev) => ({ ...prev, [field]: undefined }))
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const next = validateForm(formData)
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
          <DialogTitle>Start Reconciliation</DialogTitle>
          <DialogDescription>
            Initiate a new settlement run. Select a date and scope to begin reconciliation.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="initiate-reconciliation-form">
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
              <label htmlFor="reconciliation-settlement-date" className="text-sm font-medium">
                Settlement Date
              </label>
              <Input
                id="reconciliation-settlement-date"
                type="date"
                value={formData.settlementDate}
                onChange={(e) => handleChange('settlementDate', e.target.value)}
                aria-describedby={errors.settlementDate ? 'settlement-date-error' : undefined}
              />
              {errors.settlementDate && (
                <p id="settlement-date-error" className="text-sm text-destructive">
                  {errors.settlementDate}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="reconciliation-scope" className="text-sm font-medium">
                Scope
              </label>
              <select
                id="reconciliation-scope"
                value={formData.scope}
                onChange={(e) => handleChange('scope', e.target.value as Scope)}
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                aria-label="Scope"
              >
                <option value="ALL_ACCOUNTS">All Accounts</option>
                <option value="SELECTED_ACCOUNTS">Selected Accounts</option>
              </select>
            </div>

            {formData.scope === 'SELECTED_ACCOUNTS' && (
              <div className="space-y-1">
                <label htmlFor="reconciliation-account-id" className="text-sm font-medium">
                  Account ID
                </label>
                <Input
                  id="reconciliation-account-id"
                  value={formData.accountId}
                  onChange={(e) => handleChange('accountId', e.target.value)}
                  placeholder="acct-001"
                  aria-describedby={errors.accountId ? 'account-id-error' : undefined}
                />
                {errors.accountId && (
                  <p id="account-id-error" className="text-sm text-destructive">
                    {errors.accountId}
                  </p>
                )}
              </div>
            )}

            <div className="space-y-1">
              <label htmlFor="reconciliation-description" className="text-sm font-medium">
                Description{' '}
                <span className="font-normal text-muted-foreground">(optional)</span>
              </label>
              <Input
                id="reconciliation-description"
                value={formData.description}
                onChange={(e) => handleChange('description', e.target.value)}
                placeholder="e.g. End-of-month settlement"
                maxLength={255}
                aria-describedby={errors.description ? 'description-error' : undefined}
              />
              {errors.description && (
                <p id="description-error" className="text-sm text-destructive">
                  {errors.description}
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
            form="initiate-reconciliation-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Starting...' : 'Start Reconciliation'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
