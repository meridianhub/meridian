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
import { useCreateMapping } from './mapping-mutations'

const TARGET_SERVICE_OPTIONS = [
  { label: 'Current Account', value: 'meridian.current_account.v1.CurrentAccountService' },
  { label: 'Payment Order', value: 'meridian.payment_order.v1.PaymentOrderService' },
  { label: 'Position Keeping', value: 'meridian.position_keeping.v1.PositionKeepingService' },
  { label: 'Party', value: 'meridian.party.v1.PartyService' },
  { label: 'Internal Account', value: 'meridian.internal_account.v1.InternalAccountService' },
  { label: 'Financial Accounting', value: 'meridian.financial_accounting.v1.FinancialAccountingService' },
]

const DEFAULT_EXTERNAL_SCHEMA = `{
  "type": "object",
  "properties": {
    "amount": { "type": "number" }
  }
}`

interface FormData {
  name: string
  targetService: string
  targetRpc: string
  version: string
  externalSchema: string
}

interface FormErrors {
  name?: string
  targetService?: string
  targetRpc?: string
  version?: string
  externalSchema?: string
  general?: string
}

export interface CreateMappingDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: (mappingId: string) => void
}

export function CreateMappingDialog({
  open,
  onOpenChange,
  onSuccess,
}: CreateMappingDialogProps) {
  const createMapping = useCreateMapping()
  const [formData, setFormData] = React.useState<FormData>({
    name: '',
    targetService: '',
    targetRpc: '',
    version: '1',
    externalSchema: DEFAULT_EXTERNAL_SCHEMA,
  })
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData({
        name: '',
        targetService: '',
        targetRpc: '',
        version: '1',
        externalSchema: DEFAULT_EXTERNAL_SCHEMA,
      })
      setErrors({})
      createMapping.reset()
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  function validate(): boolean {
    const newErrors: FormErrors = {}

    if (!formData.name.trim()) {
      newErrors.name = 'Name is required'
    } else if (formData.name.trim().length > 255) {
      newErrors.name = 'Name must be 255 characters or fewer'
    }

    if (!formData.targetService) {
      newErrors.targetService = 'Target service is required'
    }

    if (!formData.targetRpc.trim()) {
      newErrors.targetRpc = 'Target RPC is required'
    } else if (formData.targetRpc.trim().length > 128) {
      newErrors.targetRpc = 'Target RPC must be 128 characters or fewer'
    }

    const versionNum = parseInt(formData.version, 10)
    if (!formData.version || isNaN(versionNum) || versionNum < 1) {
      newErrors.version = 'Version must be a positive integer'
    }

    if (formData.externalSchema.trim()) {
      try {
        JSON.parse(formData.externalSchema)
      } catch {
        newErrors.externalSchema = 'Invalid JSON: check syntax and try again'
      }
    }

    setErrors(newErrors)
    return Object.keys(newErrors).length === 0
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return

    try {
      const result = await createMapping.mutateAsync({
        name: formData.name.trim(),
        targetService: formData.targetService,
        targetRpc: formData.targetRpc.trim(),
        version: parseInt(formData.version, 10),
        externalSchema: formData.externalSchema.trim(),
      })

      const mappingId = (result as { id?: string } | null | undefined)?.id ?? ''
      onSuccess(mappingId)
      onOpenChange(false)
    } catch (err) {
      const result = handleConnectError(err)

      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'name') fieldMap.name = msg
          else if (field === 'target_service') fieldMap.targetService = msg
          else if (field === 'target_rpc') fieldMap.targetRpc = msg
          else if (field === 'version') fieldMap.version = msg
          else if (field === 'external_schema') fieldMap.externalSchema = msg
          else fieldMap.general = msg
        }
        setErrors(fieldMap)
      } else {
        setErrors({ general: result.message })
      }
    }
  }

  function handleChange(field: keyof FormData) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[600px]">
        <DialogHeader>
          <DialogTitle>Create Mapping</DialogTitle>
          <DialogDescription>
            Define a new gateway mapping between an external format and an internal Meridian service.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-mapping-form">
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
              <label htmlFor="name" className="text-sm font-medium">
                Name <span className="text-destructive">*</span>
              </label>
              <Input
                id="name"
                value={formData.name}
                onChange={handleChange('name')}
                placeholder="e.g. Stripe Webhook Inbound"
                maxLength={255}
                aria-describedby={errors.name ? 'name-error' : undefined}
              />
              {errors.name && (
                <p id="name-error" className="text-sm text-destructive">
                  {errors.name}
                </p>
              )}
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <label htmlFor="targetService" className="text-sm font-medium">
                  Target Service <span className="text-destructive">*</span>
                </label>
                <select
                  id="targetService"
                  value={formData.targetService}
                  onChange={handleChange('targetService')}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-describedby={errors.targetService ? 'targetService-error' : undefined}
                >
                  <option value="">Select service...</option>
                  {TARGET_SERVICE_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                {errors.targetService && (
                  <p id="targetService-error" className="text-sm text-destructive">
                    {errors.targetService}
                  </p>
                )}
              </div>

              <div className="space-y-1">
                <label htmlFor="version" className="text-sm font-medium">
                  Version <span className="text-destructive">*</span>
                </label>
                <Input
                  id="version"
                  type="number"
                  min={1}
                  value={formData.version}
                  onChange={handleChange('version')}
                  placeholder="1"
                  aria-describedby={errors.version ? 'version-error' : undefined}
                />
                {errors.version && (
                  <p id="version-error" className="text-sm text-destructive">
                    {errors.version}
                  </p>
                )}
              </div>
            </div>

            <div className="space-y-1">
              <label htmlFor="targetRpc" className="text-sm font-medium">
                Target RPC <span className="text-destructive">*</span>
              </label>
              <Input
                id="targetRpc"
                value={formData.targetRpc}
                onChange={handleChange('targetRpc')}
                placeholder="e.g. InitiatePaymentOrder"
                maxLength={128}
                aria-describedby={errors.targetRpc ? 'targetRpc-error' : undefined}
              />
              {errors.targetRpc && (
                <p id="targetRpc-error" className="text-sm text-destructive">
                  {errors.targetRpc}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="externalSchema" className="text-sm font-medium">
                External Schema (JSON Schema)
              </label>
              <textarea
                id="externalSchema"
                value={formData.externalSchema}
                onChange={handleChange('externalSchema')}
                rows={8}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs shadow-xs outline-none focus:border-ring"
                aria-describedby={errors.externalSchema ? 'externalSchema-error' : undefined}
                spellCheck={false}
              />
              {errors.externalSchema && (
                <p id="externalSchema-error" className="text-sm text-destructive" role="alert">
                  {errors.externalSchema}
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
            form="create-mapping-form"
            disabled={createMapping.isPending}
          >
            {createMapping.isPending ? 'Creating...' : 'Create Mapping'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
