import * as React from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
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
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { handleConnectError } from '@/lib/error-handling'
import { CATEGORY_OPTIONS, CODE_PATTERN } from './constants'

interface FormData {
  code: string
  displayName: string
  category: string
  unit: string
  description: string
}

interface FormErrors {
  code?: string
  displayName?: string
  category?: string
  unit?: string
  general?: string
}

interface RegisterDataSetDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const INITIAL_FORM: FormData = {
  code: '',
  displayName: '',
  category: '',
  unit: '',
  description: '',
}

function validate(data: FormData): FormErrors {
  const errors: FormErrors = {}

  const code = data.code.trim()
  if (!code) {
    errors.code = 'Code is required'
  } else if (code.length < 2 || code.length > 50) {
    errors.code = 'Code must be 2–50 characters'
  } else if (!CODE_PATTERN.test(code)) {
    errors.code = 'Code must start with a letter and contain only A–Z, 0–9, _ or -'
  }

  if (!data.displayName.trim()) {
    errors.displayName = 'Display name is required'
  } else if (data.displayName.trim().length > 255) {
    errors.displayName = 'Display name must be 255 characters or fewer'
  }

  if (!data.category) {
    errors.category = 'Category is required'
  }

  if (!data.unit.trim()) {
    errors.unit = 'Unit is required'
  }

  return errors
}

export function RegisterDataSetDialog({ open, onOpenChange }: RegisterDataSetDialogProps) {
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [formData, setFormData] = React.useState<FormData>(INITIAL_FORM)
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData(INITIAL_FORM)
      setErrors({})
    }
  }, [open])

  function handleChange(field: keyof FormData) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field as keyof FormErrors]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  const mutation = useMutation({
    mutationFn: () => {
      return clients.marketInformation.registerDataSet({
        code: formData.code.trim(),
        displayName: formData.displayName.trim(),
        category: parseInt(formData.category, 10),
        unit: formData.unit.trim(),
        description: formData.description.trim(),
        resolutionKeyExpression: '',
        validationExpression: '',
        errorMessageExpression: '',
      })
    },
    onSuccess: (res) => {
      queryClient.invalidateQueries({
        queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'market-data', 'datasets'],
      })
      onOpenChange(false)
      navigate(`/market-data/${res.dataset?.code ?? formData.code.trim()}`)
    },
    onError: (error: unknown) => {
      const result = handleConnectError(error)
      if (Object.keys(result.fieldErrors).length > 0) {
        const mapped: FormErrors = {}
        if (result.fieldErrors.code) mapped.code = result.fieldErrors.code
        if (result.fieldErrors.display_name) mapped.displayName = result.fieldErrors.display_name
        if (result.fieldErrors.category) mapped.category = result.fieldErrors.category
        if (result.fieldErrors.unit) mapped.unit = result.fieldErrors.unit
        setErrors(mapped)
      } else {
        setErrors({ general: result.message })
      }
    },
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const next = validate(formData)
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
          <DialogTitle>Register Data Set</DialogTitle>
          <DialogDescription>
            Create a new market data set definition in DRAFT status.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="register-dataset-form">
          <div className="space-y-4 py-2">
            <div className="space-y-1">
              <label htmlFor="dataset-code" className="text-sm font-medium">
                Code
              </label>
              <Input
                id="dataset-code"
                value={formData.code}
                onChange={handleChange('code')}
                placeholder="USD_EUR_FX"
                aria-label="Code"
                aria-describedby={errors.code ? 'dataset-code-error' : undefined}
              />
              {errors.code && (
                <p id="dataset-code-error" className="text-sm text-destructive">
                  {errors.code}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="dataset-display-name" className="text-sm font-medium">
                Display Name
              </label>
              <Input
                id="dataset-display-name"
                value={formData.displayName}
                onChange={handleChange('displayName')}
                placeholder="USD/EUR FX Rate"
                aria-label="Display Name"
                aria-describedby={errors.displayName ? 'dataset-display-name-error' : undefined}
              />
              {errors.displayName && (
                <p id="dataset-display-name-error" className="text-sm text-destructive">
                  {errors.displayName}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="dataset-category" className="text-sm font-medium">
                Category
              </label>
              <select
                id="dataset-category"
                value={formData.category}
                onChange={handleChange('category')}
                aria-label="Category"
                aria-describedby={errors.category ? 'dataset-category-error' : undefined}
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
              >
                <option value="">Select a category</option>
                {CATEGORY_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
              {errors.category && (
                <p id="dataset-category-error" className="text-sm text-destructive">
                  {errors.category}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="dataset-unit" className="text-sm font-medium">
                Unit
              </label>
              <Input
                id="dataset-unit"
                value={formData.unit}
                onChange={handleChange('unit')}
                placeholder="USD/EUR"
                aria-label="Unit"
                aria-describedby={errors.unit ? 'dataset-unit-error' : undefined}
              />
              {errors.unit && (
                <p id="dataset-unit-error" className="text-sm text-destructive">
                  {errors.unit}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="dataset-description" className="text-sm font-medium">
                Description <span className="font-normal text-muted-foreground">(optional)</span>
              </label>
              <textarea
                id="dataset-description"
                value={formData.description}
                onChange={handleChange('description')}
                placeholder="Describe this market data set..."
                maxLength={1000}
                rows={3}
                aria-label="Description"
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
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
            form="register-dataset-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Registering...' : 'Register Data Set'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
