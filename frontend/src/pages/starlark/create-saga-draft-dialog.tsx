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
import { handleConnectError } from '@/lib/error-handling'
import { referenceKeys } from '@/lib/query-keys'

const NAME_PATTERN = /^[a-z][a-z0-9_.]*$/

const STARTER_SCRIPT = `def execute(ctx):
    """Saga entry point."""
    # ctx.input contains the trigger payload
    # Use service module clients for type-safe operations
    pass
`

interface FormData {
  name: string
  displayName: string
  description: string
  script: string
  preconditionsCel: string
}

interface FormErrors {
  name?: string
  displayName?: string
  description?: string
  script?: string
  general?: string
}

export interface CreateSagaDraftDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const INITIAL_FORM: FormData = {
  name: '',
  displayName: '',
  description: '',
  script: STARTER_SCRIPT,
  preconditionsCel: '',
}

export function CreateSagaDraftDialog({ open, onOpenChange }: CreateSagaDraftDialogProps) {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [formData, setFormData] = React.useState<FormData>(INITIAL_FORM)
  const [errors, setErrors] = React.useState<FormErrors>({})

  const mutation = useMutation({
    mutationFn: () =>
      clients.sagaRegistry.createSagaDraft({
        name: formData.name.trim(),
        displayName: formData.displayName.trim(),
        description: formData.description.trim(),
        script: formData.script,
        preconditionsExpression: formData.preconditionsCel.trim(),
        version: 1,
      }),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: referenceKeys.sagas() })
      onOpenChange(false)
      const sagaId = res.saga?.id
      if (sagaId) {
        navigate(`/starlark-config/${sagaId}`)
      }
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'name') fieldMap.name = msg
          else if (field === 'display_name') fieldMap.displayName = msg
          else if (field === 'description') fieldMap.description = msg
          else if (field === 'script') fieldMap.script = msg
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
      mutation.reset()
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  function validate(): boolean {
    const next: FormErrors = {}

    const name = formData.name.trim()
    if (!name) {
      next.name = 'Name is required'
    } else if (name.length < 1 || name.length > 100) {
      next.name = 'Name must be 1–100 characters'
    } else if (!NAME_PATTERN.test(name)) {
      next.name = 'Name must start with a lowercase letter and contain only lowercase letters, digits, dots, or underscores'
    }

    if (formData.displayName.trim().length > 255) {
      next.displayName = 'Display name must be 255 characters or fewer'
    }

    if (formData.description.length > 1000) {
      next.description = 'Description must be 1000 characters or fewer'
    }

    const script = formData.script
    if (!script || script.trim().length === 0) {
      next.script = 'Script is required'
    } else if (script.length > 65536) {
      next.script = 'Script must be 65536 characters or fewer'
    }

    setErrors(next)
    return Object.keys(next).length === 0
  }

  function handleChange(field: keyof FormData) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field as keyof FormErrors]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (mutation.isPending) return
    if (!validate()) return
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
          <DialogTitle>Create Saga Draft</DialogTitle>
          <DialogDescription>
            Define a new Starlark saga workflow. It will be created in DRAFT status and must be
            activated before it can be executed.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-saga-draft-form">
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
              <label htmlFor="saga-name" className="text-sm font-medium">
                Name <span className="text-destructive">*</span>
              </label>
              <Input
                id="saga-name"
                value={formData.name}
                onChange={handleChange('name')}
                placeholder="savings.withdraw"
                aria-label="Name"
                aria-describedby={
                  errors.name ? 'saga-name-error' : 'saga-name-hint'
                }
                maxLength={100}
              />
              <p id="saga-name-hint" className="text-xs text-muted-foreground">
                Use <code className="font-mono">prefix.operation</code> format to link with an
                account type (e.g., <code className="font-mono">savings.withdraw</code>).
              </p>
              {errors.name && (
                <p id="saga-name-error" className="text-sm text-destructive">
                  {errors.name}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="saga-display-name" className="text-sm font-medium">
                Display Name{' '}
                <span className="font-normal text-muted-foreground">(optional)</span>
              </label>
              <Input
                id="saga-display-name"
                value={formData.displayName}
                onChange={handleChange('displayName')}
                placeholder="Savings Withdrawal"
                aria-label="Display Name"
                aria-describedby={errors.displayName ? 'saga-display-name-error' : undefined}
                maxLength={255}
              />
              {errors.displayName && (
                <p id="saga-display-name-error" className="text-sm text-destructive">
                  {errors.displayName}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="saga-description" className="text-sm font-medium">
                Description{' '}
                <span className="font-normal text-muted-foreground">(optional)</span>
              </label>
              <textarea
                id="saga-description"
                value={formData.description}
                onChange={handleChange('description')}
                placeholder="Describe what this saga does..."
                aria-label="Description"
                aria-describedby={errors.description ? 'saga-description-error' : undefined}
                maxLength={1000}
                rows={3}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs resize-none focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring placeholder:text-muted-foreground"
              />
              {errors.description && (
                <p id="saga-description-error" className="text-sm text-destructive">
                  {errors.description}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="saga-script" className="text-sm font-medium">
                Script <span className="text-destructive">*</span>
              </label>
              <textarea
                id="saga-script"
                value={formData.script}
                onChange={handleChange('script')}
                placeholder="def execute(ctx):&#10;    pass"
                aria-label="Script"
                aria-describedby={errors.script ? 'saga-script-error' : undefined}
                rows={12}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-sm shadow-xs resize-y focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring placeholder:text-muted-foreground"
                spellCheck={false}
              />
              {errors.script && (
                <p id="saga-script-error" className="text-sm text-destructive">
                  {errors.script}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="saga-preconditions" className="text-sm font-medium">
                Preconditions CEL{' '}
                <span className="font-normal text-muted-foreground">(optional)</span>
              </label>
              <Input
                id="saga-preconditions"
                value={formData.preconditionsCel}
                onChange={handleChange('preconditionsCel')}
                placeholder="amount > 0"
                aria-label="Preconditions CEL"
                className="font-mono text-sm"
              />
              <p className="text-xs text-muted-foreground">
                CEL expression evaluated before saga execution. Leave empty for no preconditions.
              </p>
            </div>
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="create-saga-draft-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Creating…' : 'Create Saga Draft'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
