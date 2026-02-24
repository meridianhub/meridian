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

const SOURCE_FORMAT_OPTIONS = [
  { label: 'JSON', value: 'SOURCE_FORMAT_JSON' },
  { label: 'XML', value: 'SOURCE_FORMAT_XML' },
  { label: 'CSV', value: 'SOURCE_FORMAT_CSV' },
  { label: 'ISO 20022', value: 'SOURCE_FORMAT_ISO20022' },
]

const TARGET_SERVICE_OPTIONS = [
  { label: 'Current Account', value: 'meridian.current_account.v1.CurrentAccountService' },
  { label: 'Payment Order', value: 'meridian.payment_order.v1.PaymentOrderService' },
  { label: 'Position Keeping', value: 'meridian.position_keeping.v1.PositionKeepingService' },
  { label: 'Party', value: 'meridian.party.v1.PartyService' },
  { label: 'Internal Bank Account', value: 'meridian.internal_bank_account.v1.InternalBankAccountService' },
  { label: 'Financial Accounting', value: 'meridian.financial_accounting.v1.FinancialAccountingService' },
]

const DEFAULT_MAPPING_RULES = `{
  "fieldMappings": [
    { "source": "$.amount", "target": "amount.value" }
  ]
}`

interface FormData {
  name: string
  sourceFormat: string
  targetService: string
  description: string
  mappingRules: string
}

interface FormErrors {
  name?: string
  sourceFormat?: string
  targetService?: string
  description?: string
  mappingRules?: string
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
    sourceFormat: '',
    targetService: '',
    description: '',
    mappingRules: DEFAULT_MAPPING_RULES,
  })
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData({
        name: '',
        sourceFormat: '',
        targetService: '',
        description: '',
        mappingRules: DEFAULT_MAPPING_RULES,
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

    if (!formData.sourceFormat) {
      newErrors.sourceFormat = 'Source format is required'
    }

    if (!formData.targetService) {
      newErrors.targetService = 'Target service is required'
    }

    if (formData.description.length > 1000) {
      newErrors.description = 'Description must be 1000 characters or fewer'
    }

    if (!formData.mappingRules.trim()) {
      newErrors.mappingRules = 'Mapping rules are required'
    } else {
      try {
        JSON.parse(formData.mappingRules)
      } catch {
        newErrors.mappingRules = 'Invalid JSON: check syntax and try again'
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
        sourceFormat: formData.sourceFormat,
        targetService: formData.targetService,
        description: formData.description.trim(),
        mappingRules: formData.mappingRules.trim(),
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
          else if (field === 'source_format') fieldMap.sourceFormat = msg
          else if (field === 'target_service') fieldMap.targetService = msg
          else if (field === 'description') fieldMap.description = msg
          else if (field === 'mapping_rules') fieldMap.mappingRules = msg
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
                <label htmlFor="sourceFormat" className="text-sm font-medium">
                  Source Format <span className="text-destructive">*</span>
                </label>
                <select
                  id="sourceFormat"
                  value={formData.sourceFormat}
                  onChange={handleChange('sourceFormat')}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-describedby={errors.sourceFormat ? 'sourceFormat-error' : undefined}
                >
                  <option value="">Select format...</option>
                  {SOURCE_FORMAT_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                {errors.sourceFormat && (
                  <p id="sourceFormat-error" className="text-sm text-destructive">
                    {errors.sourceFormat}
                  </p>
                )}
              </div>

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
            </div>

            <div className="space-y-1">
              <label htmlFor="description" className="text-sm font-medium">
                Description
              </label>
              <textarea
                id="description"
                value={formData.description}
                onChange={handleChange('description')}
                placeholder="Optional description for this mapping..."
                maxLength={1000}
                rows={2}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs outline-none focus:border-ring"
                aria-describedby={errors.description ? 'description-error' : undefined}
              />
              {errors.description && (
                <p id="description-error" className="text-sm text-destructive">
                  {errors.description}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="mappingRules" className="text-sm font-medium">
                Mapping Rules <span className="text-destructive">*</span>
              </label>
              <textarea
                id="mappingRules"
                value={formData.mappingRules}
                onChange={handleChange('mappingRules')}
                rows={8}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs shadow-xs outline-none focus:border-ring"
                aria-describedby={errors.mappingRules ? 'mappingRules-error' : undefined}
                spellCheck={false}
              />
              {errors.mappingRules && (
                <p id="mappingRules-error" className="text-sm text-destructive" role="alert">
                  {errors.mappingRules}
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
