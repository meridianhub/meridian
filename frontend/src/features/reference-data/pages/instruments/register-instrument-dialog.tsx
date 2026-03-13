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
import { useApiClients } from '@/api/context'
import { handleConnectError } from '@/lib/error-handling'
import { referenceKeys } from '@/lib/query-keys'
import { Code } from '@connectrpc/connect'

const CODE_PATTERN = /^[A-Z][A-Z0-9_]*$/

const DIMENSION_OPTIONS = [
  { label: 'Currency', value: 1 },
  { label: 'Energy', value: 2 },
  { label: 'Mass', value: 3 },
  { label: 'Volume', value: 4 },
  { label: 'Time', value: 5 },
  { label: 'Compute', value: 6 },
  { label: 'Carbon', value: 7 },
  { label: 'Data', value: 8 },
  { label: 'Count', value: 9 },
]

interface FormData {
  code: string
  displayName: string
  dimension: string
  decimalPlaces: string
  description: string
}

interface FormErrors {
  code?: string
  displayName?: string
  dimension?: string
  decimalPlaces?: string
  description?: string
  general?: string
}

export interface RegisterInstrumentDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const INITIAL_FORM: FormData = {
  code: '',
  displayName: '',
  dimension: '',
  decimalPlaces: '0',
  description: '',
}

export function RegisterInstrumentDialog({ open, onOpenChange }: RegisterInstrumentDialogProps) {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const [formData, setFormData] = React.useState<FormData>(INITIAL_FORM)
  const [errors, setErrors] = React.useState<FormErrors>({})
  const [successMessage, setSuccessMessage] = React.useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: () =>
      clients.referenceData.registerInstrument({
        code: formData.code.trim(),
        displayName: formData.displayName.trim(),
        dimension: Number(formData.dimension),
        precision: Number(formData.decimalPlaces),
        description: formData.description.trim(),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: referenceKeys.instruments() })
      setFormData(INITIAL_FORM)
      setErrors({})
      setSuccessMessage(
        'Instrument created in DRAFT status. Activation required via manifest or API.',
      )
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'code') fieldMap.code = msg
          else if (field === 'display_name') fieldMap.displayName = msg
          else if (field === 'dimension') fieldMap.dimension = msg
          else if (field === 'precision') fieldMap.decimalPlaces = msg
          else if (field === 'description') fieldMap.description = msg
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

    if (!formData.code.trim()) {
      next.code = 'Code is required'
    } else if (!CODE_PATTERN.test(formData.code.trim())) {
      next.code = 'Invalid code format — must start with a letter, uppercase letters, digits, and underscores only'
    } else if (formData.code.trim().length < 2 || formData.code.trim().length > 20) {
      next.code = 'Code must be between 2 and 20 characters'
    }

    if (!formData.displayName.trim()) {
      next.displayName = 'Display name is required'
    } else if (formData.displayName.trim().length > 255) {
      next.displayName = 'Display name must be 255 characters or fewer'
    }

    if (!formData.dimension) {
      next.dimension = 'Dimension is required'
    }

    const dp = Number(formData.decimalPlaces)
    if (formData.decimalPlaces === '' || isNaN(dp) || !Number.isInteger(dp)) {
      next.decimalPlaces = 'Decimal places must be a whole number'
    } else if (dp < 0 || dp > 18) {
      next.decimalPlaces = 'Decimal places must be between 0 and 18'
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

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    setSuccessMessage(null)
    mutation.mutate()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Register Instrument</DialogTitle>
          <DialogDescription>
            Create a new instrument definition. It will be created in DRAFT status and must be
            activated before it can be used.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="register-instrument-form">
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

            <div className="space-y-1">
              <label htmlFor="instrument-code" className="text-sm font-medium">
                Code <span className="text-destructive">*</span>
              </label>
              <Input
                id="instrument-code"
                value={formData.code}
                onChange={handleCodeChange}
                placeholder="KWH"
                aria-label="Code"
                aria-describedby={errors.code ? 'instrument-code-error' : undefined}
                maxLength={20}
              />
              {errors.code && (
                <p id="instrument-code-error" className="text-sm text-destructive">
                  {errors.code}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="instrument-display-name" className="text-sm font-medium">
                Display Name <span className="text-destructive">*</span>
              </label>
              <Input
                id="instrument-display-name"
                value={formData.displayName}
                onChange={handleChange('displayName')}
                placeholder="Kilowatt Hour"
                aria-label="Display Name"
                aria-describedby={errors.displayName ? 'instrument-display-name-error' : undefined}
                maxLength={255}
              />
              {errors.displayName && (
                <p id="instrument-display-name-error" className="text-sm text-destructive">
                  {errors.displayName}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="instrument-dimension" className="text-sm font-medium">
                Dimension <span className="text-destructive">*</span>
              </label>
              <select
                id="instrument-dimension"
                value={formData.dimension}
                onChange={handleChange('dimension')}
                aria-label="Dimension"
                aria-describedby={errors.dimension ? 'instrument-dimension-error' : undefined}
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
              >
                <option value="">Select a dimension…</option>
                {DIMENSION_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
              {errors.dimension && (
                <p id="instrument-dimension-error" className="text-sm text-destructive">
                  {errors.dimension}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="instrument-decimal-places" className="text-sm font-medium">
                Decimal Places <span className="text-destructive">*</span>
              </label>
              <Input
                id="instrument-decimal-places"
                inputMode="numeric"
                value={formData.decimalPlaces}
                onChange={handleChange('decimalPlaces')}
                placeholder="0"
                aria-label="Decimal Places"
                aria-describedby={errors.decimalPlaces ? 'instrument-decimal-places-error' : 'instrument-decimal-places-hint'}
              />
              <p id="instrument-decimal-places-hint" className="text-xs text-muted-foreground">
                Controls the precision for amounts (0 = whole numbers, 18 = maximum precision).
              </p>
              {errors.decimalPlaces && (
                <p id="instrument-decimal-places-error" className="text-sm text-destructive">
                  {errors.decimalPlaces}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="instrument-description" className="text-sm font-medium">
                Description
              </label>
              <textarea
                id="instrument-description"
                value={formData.description}
                onChange={handleChange('description')}
                placeholder="Optional description of this instrument"
                aria-label="Description"
                aria-describedby={errors.description ? 'instrument-description-error' : undefined}
                maxLength={1000}
                rows={3}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs resize-none focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
              {errors.description && (
                <p id="instrument-description-error" className="text-sm text-destructive">
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
            form="register-instrument-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Registering…' : 'Register Instrument'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
