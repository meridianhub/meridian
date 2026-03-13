import * as React from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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
import { CELEditor } from '@/features/sagas/components/cel-editor'
import { useApiClients } from '@/api/context'
import { handleConnectError } from '@/lib/error-handling'
import { referenceKeys } from '@/lib/query-keys'
import { Code } from '@connectrpc/connect'

// Matches proto: ^[A-Z][A-Z0-9_]{0,48}[A-Z0-9]$ (2-50 chars, no trailing underscore)
const CODE_PATTERN = /^[A-Z][A-Z0-9_]{0,48}[A-Z0-9]$/
const SAGA_PREFIX_PATTERN = /^[a-z][a-z0-9_]*$/

const NORMAL_BALANCE_OPTIONS = [
  { label: 'Debit', value: 1 },
  { label: 'Credit', value: 2 },
]

const BEHAVIOR_CLASS_OPTIONS = [
  { label: 'Customer', value: 1 },
  { label: 'Clearing', value: 2 },
  { label: 'Nostro', value: 3 },
  { label: 'Vostro', value: 4 },
  { label: 'Holding', value: 5 },
  { label: 'Suspense', value: 6 },
  { label: 'Revenue', value: 7 },
  { label: 'Expense', value: 8 },
  { label: 'Inventory', value: 9 },
]

interface FormData {
  code: string
  displayName: string
  normalBalance: string
  behaviorClass: string
  instrumentCode: string
  defaultSagaPrefix: string
  description: string
  validationCel: string
  bucketingCel: string
  eligibilityCel: string
}

interface FormErrors {
  code?: string
  displayName?: string
  normalBalance?: string
  behaviorClass?: string
  instrumentCode?: string
  defaultSagaPrefix?: string
  description?: string
  validationCel?: string
  bucketingCel?: string
  eligibilityCel?: string
  general?: string
}

export interface CreateAccountTypeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const INITIAL_FORM: FormData = {
  code: '',
  displayName: '',
  normalBalance: '',
  behaviorClass: '',
  instrumentCode: '',
  defaultSagaPrefix: '',
  description: '',
  validationCel: '',
  bucketingCel: '',
  eligibilityCel: '',
}

export function CreateAccountTypeDialog({ open, onOpenChange }: CreateAccountTypeDialogProps) {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const [formData, setFormData] = React.useState<FormData>(INITIAL_FORM)
  const [errors, setErrors] = React.useState<FormErrors>({})
  const [successMessage, setSuccessMessage] = React.useState<string | null>(null)

  const { data: instrumentsData, isLoading: instrumentsLoading, isError: instrumentsError } = useQuery({
    queryKey: [...referenceKeys.instruments(), 'active'],
    queryFn: async () => {
      const res = await clients.referenceData.listInstruments({ statusFilter: 2 })
      return res.instruments ?? []
    },
    enabled: open,
  })

  const instruments = instrumentsData ?? []

  const mutation = useMutation({
    mutationFn: () =>
      clients.accountTypeRegistry.createDraft({
        code: formData.code.trim(),
        displayName: formData.displayName.trim(),
        normalBalance: Number(formData.normalBalance),
        behaviorClass: Number(formData.behaviorClass),
        instrumentCode: formData.instrumentCode,
        defaultSagaPrefix: formData.defaultSagaPrefix.trim(),
        description: formData.description.trim(),
        validationCel: formData.validationCel,
        bucketingCel: formData.bucketingCel,
        eligibilityCel: formData.eligibilityCel,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: referenceKeys.accountTypes() })
      setFormData(INITIAL_FORM)
      setErrors({})
      setSuccessMessage(
        'Account type created in DRAFT status. Activation required before it can be used.',
      )
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'code') fieldMap.code = msg
          else if (field === 'display_name') fieldMap.displayName = msg
          else if (field === 'normal_balance') fieldMap.normalBalance = msg
          else if (field === 'behavior_class') fieldMap.behaviorClass = msg
          else if (field === 'instrument_code') fieldMap.instrumentCode = msg
          else if (field === 'default_saga_prefix') fieldMap.defaultSagaPrefix = msg
          else if (field === 'description') fieldMap.description = msg
          else if (field === 'validation_cel') fieldMap.validationCel = msg
          else if (field === 'bucketing_cel') fieldMap.bucketingCel = msg
          else if (field === 'eligibility_cel') fieldMap.eligibilityCel = msg
          else fieldMap.general = msg
        }
        setErrors(fieldMap)
      } else {
        setErrors({ general: result.message })
      }
    },
  })

  React.useEffect(() => {
    if (!open) {
      setFormData(INITIAL_FORM)
      setErrors({})
      setSuccessMessage(null)
      mutation.reset()
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  function validate(): boolean {
    const next: FormErrors = {}

    const code = formData.code.trim()
    if (!code) {
      next.code = 'Code is required'
    } else if (!CODE_PATTERN.test(code)) {
      next.code = 'Invalid code format — must start and end with an uppercase letter or digit, with uppercase letters, digits, and underscores only (2–50 characters)'
    }

    if (!formData.displayName.trim()) {
      next.displayName = 'Display name is required'
    } else if (formData.displayName.trim().length > 255) {
      next.displayName = 'Display name must be 255 characters or fewer'
    }

    if (!formData.normalBalance) {
      next.normalBalance = 'Normal balance is required'
    }

    if (!formData.behaviorClass) {
      next.behaviorClass = 'Behavior class is required'
    }

    if (!formData.instrumentCode) {
      next.instrumentCode = 'Instrument is required'
    }

    const sagaPrefix = formData.defaultSagaPrefix.trim()
    if (sagaPrefix && !SAGA_PREFIX_PATTERN.test(sagaPrefix)) {
      next.defaultSagaPrefix = 'Saga prefix must start with a lowercase letter, followed by lowercase letters, digits, and underscores only'
    }

    if (formData.description && formData.description.length > 1000) {
      next.description = 'Description must be 1000 characters or fewer'
    }

    setErrors(next)
    return Object.keys(next).length === 0
  }

  function handleCodeChange(e: React.ChangeEvent<HTMLInputElement>) {
    const upper = e.target.value.toUpperCase()
    setFormData((prev) => ({ ...prev, code: upper }))
    if (errors.code) setErrors((prev) => ({ ...prev, code: undefined }))
  }

  function handleChange(field: keyof FormData) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field]) setErrors((prev) => ({ ...prev, [field]: undefined }))
    }
  }

  function handleCelChange(field: 'validationCel' | 'bucketingCel' | 'eligibilityCel') {
    return (value: string) => {
      setFormData((prev) => ({ ...prev, [field]: value }))
      if (errors[field]) setErrors((prev) => ({ ...prev, [field]: undefined }))
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (mutation.isPending) return
    if (!validate()) return
    setSuccessMessage(null)
    mutation.mutate()
  }

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen && mutation.isPending) return
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Create Account Type</DialogTitle>
          <DialogDescription>
            Define a new account type. It will be created in DRAFT status and must be activated
            before it can be used.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-account-type-form">
          <div className="space-y-4 py-2">
            {errors.general && (
              <div
                role="alert"
                className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {errors.general}
              </div>
            )}

            {successMessage && (
              <div
                role="status"
                className="rounded-md border border-success/50 bg-success-muted px-3 py-2 text-sm text-success-foreground"
              >
                {successMessage}
              </div>
            )}

            {/* Primary fields - two column layout */}
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1">
                <label htmlFor="account-type-code" className="text-sm font-medium">
                  Code <span className="text-destructive">*</span>
                </label>
                <Input
                  id="account-type-code"
                  value={formData.code}
                  onChange={handleCodeChange}
                  placeholder="CUSTOMER_CURRENT"
                  aria-label="Code"
                  aria-describedby={errors.code ? 'account-type-code-error' : undefined}
                  maxLength={50}
                />
                {errors.code && (
                  <p id="account-type-code-error" className="text-sm text-destructive">
                    {errors.code}
                  </p>
                )}
              </div>

              <div className="space-y-1">
                <label htmlFor="account-type-display-name" className="text-sm font-medium">
                  Display Name <span className="text-destructive">*</span>
                </label>
                <Input
                  id="account-type-display-name"
                  value={formData.displayName}
                  onChange={handleChange('displayName')}
                  placeholder="Customer Current Account"
                  aria-label="Display Name"
                  aria-describedby={errors.displayName ? 'account-type-display-name-error' : undefined}
                  maxLength={255}
                />
                {errors.displayName && (
                  <p id="account-type-display-name-error" className="text-sm text-destructive">
                    {errors.displayName}
                  </p>
                )}
              </div>

              <div className="space-y-1">
                <label htmlFor="account-type-normal-balance" className="text-sm font-medium">
                  Normal Balance <span className="text-destructive">*</span>
                </label>
                <select
                  id="account-type-normal-balance"
                  value={formData.normalBalance}
                  onChange={handleChange('normalBalance')}
                  aria-label="Normal Balance"
                  aria-describedby={errors.normalBalance ? 'account-type-normal-balance-error' : undefined}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                >
                  <option value="">Select normal balance…</option>
                  {NORMAL_BALANCE_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                {errors.normalBalance && (
                  <p id="account-type-normal-balance-error" className="text-sm text-destructive">
                    {errors.normalBalance}
                  </p>
                )}
              </div>

              <div className="space-y-1">
                <label htmlFor="account-type-behavior-class" className="text-sm font-medium">
                  Behavior Class <span className="text-destructive">*</span>
                </label>
                <select
                  id="account-type-behavior-class"
                  value={formData.behaviorClass}
                  onChange={handleChange('behaviorClass')}
                  aria-label="Behavior Class"
                  aria-describedby={errors.behaviorClass ? 'account-type-behavior-class-error' : undefined}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                >
                  <option value="">Select behavior class…</option>
                  {BEHAVIOR_CLASS_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                {errors.behaviorClass && (
                  <p id="account-type-behavior-class-error" className="text-sm text-destructive">
                    {errors.behaviorClass}
                  </p>
                )}
              </div>

              <div className="space-y-1">
                <label htmlFor="account-type-instrument" className="text-sm font-medium">
                  Instrument <span className="text-destructive">*</span>
                </label>
                {instrumentsLoading ? (
                  <select
                    id="account-type-instrument"
                    disabled
                    className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                    aria-busy="true"
                  >
                    <option value="">Loading instruments…</option>
                  </select>
                ) : instrumentsError ? (
                  <select
                    id="account-type-instrument"
                    disabled
                    className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                    aria-describedby="account-type-instrument-load-error"
                  >
                    <option value="">Failed to load instruments</option>
                  </select>
                ) : (
                  <select
                    id="account-type-instrument"
                    value={formData.instrumentCode}
                    onChange={handleChange('instrumentCode')}
                    aria-label="Instrument"
                    aria-describedby={errors.instrumentCode ? 'account-type-instrument-error' : undefined}
                    className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  >
                    <option value="">Select an instrument…</option>
                    {instruments.map((inst) => (
                      <option key={inst.code} value={inst.code}>
                        {inst.code}{inst.displayName ? ` — ${inst.displayName}` : ''}
                      </option>
                    ))}
                  </select>
                )}
                {instrumentsError && (
                  <p id="account-type-instrument-load-error" className="text-sm text-destructive">
                    Unable to load instruments. Please close and reopen the dialog to retry.
                  </p>
                )}
                {errors.instrumentCode && (
                  <p id="account-type-instrument-error" className="text-sm text-destructive">
                    {errors.instrumentCode}
                  </p>
                )}
              </div>

              <div className="space-y-1">
                <label htmlFor="account-type-saga-prefix" className="text-sm font-medium">
                  Default Saga Prefix
                </label>
                <Input
                  id="account-type-saga-prefix"
                  value={formData.defaultSagaPrefix}
                  onChange={handleChange('defaultSagaPrefix')}
                  placeholder="savings"
                  aria-label="Default Saga Prefix"
                  aria-describedby={
                    errors.defaultSagaPrefix
                      ? 'account-type-saga-prefix-error'
                      : 'account-type-saga-prefix-hint'
                  }
                />
                <p id="account-type-saga-prefix-hint" className="text-xs text-muted-foreground">
                  Sagas for this account type will be resolved as{' '}
                  <code className="font-mono">{'{prefix}.{operation}'}</code> (e.g.,{' '}
                  <code className="font-mono">savings.withdraw</code>). Leave empty to use generic saga names.
                </p>
                {errors.defaultSagaPrefix && (
                  <p id="account-type-saga-prefix-error" className="text-sm text-destructive">
                    {errors.defaultSagaPrefix}
                  </p>
                )}
              </div>
            </div>

            <div className="space-y-1">
              <label htmlFor="account-type-description" className="text-sm font-medium">
                Description
              </label>
              <textarea
                id="account-type-description"
                value={formData.description}
                onChange={handleChange('description')}
                placeholder="Optional description of this account type"
                aria-label="Description"
                aria-describedby={errors.description ? 'account-type-description-error' : undefined}
                maxLength={1000}
                rows={3}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs resize-none focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
              {errors.description && (
                <p id="account-type-description-error" className="text-sm text-destructive">
                  {errors.description}
                </p>
              )}
            </div>

            {/* CEL editors - full width */}
            <div className="space-y-2">
              <label className="text-sm font-medium">Validation CEL</label>
              <p className="text-xs text-muted-foreground">
                Validates account operations. Available: amount, attributes, valid_from, valid_to, source.
              </p>
              <CELEditor
                value={formData.validationCel}
                onChange={handleCelChange('validationCel')}
                context="validation"
                errors={errors.validationCel ? [{ message: errors.validationCel }] : []}
              />
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium">Bucketing CEL</label>
              <p className="text-xs text-muted-foreground">
                Determines fungibility buckets for amount pooling. Available: attributes.
              </p>
              <CELEditor
                value={formData.bucketingCel}
                onChange={handleCelChange('bucketingCel')}
                context="bucketKey"
                errors={errors.bucketingCel ? [{ message: errors.bucketingCel }] : []}
              />
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium">Eligibility CEL</label>
              <p className="text-xs text-muted-foreground">
                Determines account eligibility for operations. Available: party.type, party.status, party.external_reference_type, attributes.
              </p>
              <CELEditor
                value={formData.eligibilityCel}
                onChange={handleCelChange('eligibilityCel')}
                context="eligibility"
                errors={errors.eligibilityCel ? [{ message: errors.eligibilityCel }] : []}
              />
            </div>
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="create-account-type-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Creating…' : 'Create Account Type'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
