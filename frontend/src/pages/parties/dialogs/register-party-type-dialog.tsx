import * as React from 'react'
import { Code } from '@connectrpc/connect'
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
import { tenantKeys } from '@/lib/query-keys'
import { useTenantSlug } from '@/hooks/use-tenant-context'

// Pattern: uppercase letter followed by uppercase letters, digits, underscores
const PARTY_TYPE_CODE_PATTERN = /^[A-Z][A-Z0-9_]*$/

interface FormData {
  code: string
  description: string
}

interface FormErrors {
  code?: string
  description?: string
  general?: string
}

const initialFormData: FormData = {
  code: '',
  description: '',
}

export interface RegisterPartyTypeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function RegisterPartyTypeDialog({ open, onOpenChange }: RegisterPartyTypeDialogProps) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()
  const queryClient = useQueryClient()

  const [formData, setFormData] = React.useState<FormData>(initialFormData)
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData(initialFormData)
      setErrors({})
    }
  }, [open])

  const mutation = useMutation({
    mutationFn: async () => {
      return clients.party.registerPartyType({
        partyType: formData.code.trim(),
        attributeSchema: formData.description.trim()
          ? JSON.stringify({ description: formData.description.trim() })
          : '',
        validationCel: '',
        eligibilityCel: '',
        errorMessageCel: '',
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tenantKeys.partyTypes(tenantSlug ?? '') })
      onOpenChange(false)
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'party_type') fieldMap.code = msg
          else fieldMap.general = msg
        }
        setErrors(fieldMap)
      } else if (result.code === Code.AlreadyExists) {
        setErrors({ code: 'A party type with this code already exists' })
      } else {
        setErrors({ general: result.message })
      }
    },
  })

  function validate(): boolean {
    const newErrors: FormErrors = {}

    const code = formData.code.trim()
    if (!code) {
      newErrors.code = 'Code is required'
    } else if (code.length < 2) {
      newErrors.code = 'Code must be at least 2 characters'
    } else if (code.length > 50) {
      newErrors.code = 'Code must be 50 characters or fewer'
    } else if (!PARTY_TYPE_CODE_PATTERN.test(code)) {
      newErrors.code = 'Code must start with an uppercase letter and contain only uppercase letters, numbers, and underscores'
    }

    if (formData.description.trim().length > 1000) {
      newErrors.description = 'Description must be 1000 characters or fewer'
    }

    setErrors(newErrors)
    return Object.keys(newErrors).length === 0
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    mutation.mutate()
  }

  function handleCodeChange(e: React.ChangeEvent<HTMLInputElement>) {
    const upper = e.target.value.toUpperCase()
    setFormData((prev) => ({ ...prev, code: upper }))
    if (errors.code) {
      setErrors((prev) => ({ ...prev, code: undefined }))
    }
  }

  function handleDescriptionChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    setFormData((prev) => ({ ...prev, description: e.target.value }))
    if (errors.description) {
      setErrors((prev) => ({ ...prev, description: undefined }))
    }
  }

  const descriptionLength = formData.description.length
  const descriptionMax = 1000

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Party Type</DialogTitle>
          <DialogDescription>
            Define a new party type for this tenant. The code uniquely identifies the type.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="register-party-type-form">
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
              <label htmlFor="partyTypeCode" className="text-sm font-medium">
                Code <span aria-hidden="true">*</span>
              </label>
              <Input
                id="partyTypeCode"
                value={formData.code}
                onChange={handleCodeChange}
                placeholder="INDIVIDUAL"
                aria-describedby={
                  errors.code ? 'partyTypeCode-error' : 'partyTypeCode-hint'
                }
                aria-required="true"
              />
              <p id="partyTypeCode-hint" className="text-xs text-muted-foreground">
                Uppercase letters, numbers, underscores. Must start with a letter. (2–50 chars)
              </p>
              {errors.code && (
                <p id="partyTypeCode-error" role="alert" className="text-sm text-destructive">
                  {errors.code}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="partyTypeDescription" className="text-sm font-medium">
                Description
              </label>
              <textarea
                id="partyTypeDescription"
                value={formData.description}
                onChange={handleDescriptionChange}
                placeholder="Optional description of this party type"
                rows={3}
                maxLength={descriptionMax}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                aria-describedby={
                  errors.description
                    ? 'partyTypeDescription-error partyTypeDescription-count'
                    : 'partyTypeDescription-count'
                }
              />
              <p
                id="partyTypeDescription-count"
                className="text-xs text-muted-foreground"
                aria-live="polite"
                aria-label={`${descriptionLength} of ${descriptionMax} characters used`}
              >
                {descriptionLength}/{descriptionMax}
              </p>
              {errors.description && (
                <p id="partyTypeDescription-error" role="alert" className="text-sm text-destructive">
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
            form="register-party-type-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Adding...' : 'Add Party Type'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
