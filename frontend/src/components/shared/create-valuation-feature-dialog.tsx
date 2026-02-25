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
import { handleConnectError } from '@/lib/error-handling'
import { Code, ConnectError } from '@connectrpc/connect'

export type AccountType = 'current' | 'internal'

export interface CreateValuationFeatureDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  accountId: string
  accountType: AccountType
  accountCurrency: string
}

interface FormData {
  instrumentCode: string
  valuationMethodId: string
  valuationMethodVersion: string
  outputInstrument: string
  parameters: string
}

interface FormErrors {
  instrumentCode?: string
  valuationMethodId?: string
  valuationMethodVersion?: string
  outputInstrument?: string
  parameters?: string
  general?: string
}

const initialFormData: FormData = {
  instrumentCode: '',
  valuationMethodId: '',
  valuationMethodVersion: '',
  outputInstrument: '',
  parameters: '',
}

async function createValuationFeature(
  tenantSlug: string,
  accountType: AccountType,
  accountId: string,
  instrumentCode: string,
  valuationMethodId: string,
  valuationMethodVersion: number,
  outputInstrument: string,
  parameters: string,
): Promise<string> {
  const serviceName =
    accountType === 'current'
      ? 'meridian.current_account.v1.CurrentAccountService'
      : 'meridian.internal_bank_account.v1.InternalBankAccountService'

  const body: Record<string, unknown> = {
    accountId,
    instrumentCode,
    valuationMethodId,
    valuationMethodVersion,
    outputInstrument,
  }

  if (parameters.trim()) {
    body.parameters = parameters.trim()
  }

  const response = await fetch(`/api/${serviceName}/CreateValuationFeature`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Tenant-Slug': tenantSlug,
    },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as {
      message?: string
      code?: number
      details?: unknown[]
    }
    const err = new ConnectError(
      data.message ?? `Failed to create valuation feature: ${response.status}`,
      data.code ?? Code.Unknown,
    )
    throw err
  }

  const data = (await response.json()) as { feature?: { id?: string } }
  const featureId = data.feature?.id
  if (!featureId) {
    throw new ConnectError('Feature ID missing from response', Code.Internal)
  }
  return featureId
}

function validateForm(formData: FormData, accountCurrency: string): FormErrors {
  const errors: FormErrors = {}

  if (!formData.instrumentCode.trim()) {
    errors.instrumentCode = 'Instrument code is required'
  }

  if (!formData.valuationMethodId.trim()) {
    errors.valuationMethodId = 'Valuation method ID is required'
  }

  const version = parseInt(formData.valuationMethodVersion, 10)
  if (!formData.valuationMethodVersion.trim()) {
    errors.valuationMethodVersion = 'Version is required'
  } else if (isNaN(version) || version < 1) {
    errors.valuationMethodVersion = 'Version must be a whole number of 1 or greater'
  }

  if (!formData.outputInstrument.trim()) {
    errors.outputInstrument = 'Output instrument is required'
  } else if (formData.outputInstrument.trim() !== accountCurrency) {
    errors.outputInstrument = `Output instrument must match the account currency (${accountCurrency})`
  }

  if (formData.parameters.trim()) {
    try {
      JSON.parse(formData.parameters.trim())
    } catch {
      errors.parameters = 'Parameters must be valid JSON'
    }
  }

  return errors
}

function hasErrors(errors: FormErrors): boolean {
  return Object.keys(errors).length > 0
}

export function CreateValuationFeatureDialog({
  open,
  onOpenChange,
  accountId,
  accountType,
  accountCurrency,
}: CreateValuationFeatureDialogProps) {
  const { tenantSlug } = useTenantContext()
  const queryClient = useQueryClient()

  const [formData, setFormData] = React.useState<FormData>(initialFormData)
  const [errors, setErrors] = React.useState<FormErrors>({})
  const [successFeatureId, setSuccessFeatureId] = React.useState<string | null>(null)

  React.useEffect(() => {
    if (!open) {
      setFormData(initialFormData)
      setErrors({})
      setSuccessFeatureId(null)
    }
  }, [open])

  const mutation = useMutation({
    mutationFn: () => {
      const version = parseInt(formData.valuationMethodVersion, 10)
      return createValuationFeature(
        tenantSlug ?? '',
        accountType,
        accountId,
        formData.instrumentCode.trim(),
        formData.valuationMethodId.trim(),
        version,
        formData.outputInstrument.trim(),
        formData.parameters,
      )
    },
    onSuccess: (featureId) => {
      if (accountType === 'current') {
        queryClient.invalidateQueries({
          queryKey: tenantKeys.account(tenantSlug ?? '', accountId),
        })
      } else {
        queryClient.invalidateQueries({
          queryKey: tenantKeys.internalAccount(tenantSlug ?? '', accountId),
        })
      }
      setSuccessFeatureId(featureId)
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'instrument_code') fieldMap.instrumentCode = msg
          else if (field === 'valuation_method_id') fieldMap.valuationMethodId = msg
          else if (field === 'valuation_method_version') fieldMap.valuationMethodVersion = msg
          else if (field === 'output_instrument') fieldMap.outputInstrument = msg
          else if (field === 'parameters') fieldMap.parameters = msg
          else fieldMap.general = msg
        }
        setErrors(fieldMap)
      } else {
        setErrors({ general: result.message })
      }
    },
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const validationErrors = validateForm(formData, accountCurrency)
    if (hasErrors(validationErrors)) {
      setErrors(validationErrors)
      return
    }
    setErrors({})
    mutation.mutate()
  }

  function handleChange(field: keyof FormData) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  if (successFeatureId) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Valuation Feature Created</DialogTitle>
            <DialogDescription>
              The valuation feature has been successfully created for account {accountId}.
            </DialogDescription>
          </DialogHeader>
          <div className="py-2">
            <p className="text-sm text-muted-foreground">Feature ID</p>
            <p className="font-mono text-sm" data-testid="feature-id">
              {successFeatureId}
            </p>
          </div>
          <DialogFooter>
            <Button onClick={() => onOpenChange(false)}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Valuation Feature</DialogTitle>
          <DialogDescription>
            Map an input instrument to the account&apos;s native instrument ({accountCurrency}) using
            a valuation method.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-valuation-feature-form">
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
              <label htmlFor="instrumentCode" className="text-sm font-medium">
                Input Instrument Code <span aria-hidden="true">*</span>
              </label>
              <Input
                id="instrumentCode"
                value={formData.instrumentCode}
                onChange={handleChange('instrumentCode')}
                placeholder="e.g. USD, EUR, kWh"
                aria-describedby={errors.instrumentCode ? 'instrumentCode-error' : undefined}
                aria-required="true"
              />
              {errors.instrumentCode && (
                <p id="instrumentCode-error" className="text-sm text-destructive">
                  {errors.instrumentCode}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="valuationMethodId" className="text-sm font-medium">
                Valuation Method ID <span aria-hidden="true">*</span>
              </label>
              <Input
                id="valuationMethodId"
                value={formData.valuationMethodId}
                onChange={handleChange('valuationMethodId')}
                placeholder="e.g. fx-rate-usd-gbp"
                aria-describedby={errors.valuationMethodId ? 'valuationMethodId-error' : undefined}
                aria-required="true"
              />
              {errors.valuationMethodId && (
                <p id="valuationMethodId-error" className="text-sm text-destructive">
                  {errors.valuationMethodId}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="valuationMethodVersion" className="text-sm font-medium">
                Method Version <span aria-hidden="true">*</span>
              </label>
              <Input
                id="valuationMethodVersion"
                value={formData.valuationMethodVersion}
                onChange={handleChange('valuationMethodVersion')}
                placeholder="e.g. 1"
                inputMode="numeric"
                aria-describedby={
                  errors.valuationMethodVersion ? 'valuationMethodVersion-error' : undefined
                }
                aria-required="true"
              />
              {errors.valuationMethodVersion && (
                <p id="valuationMethodVersion-error" className="text-sm text-destructive">
                  {errors.valuationMethodVersion}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="outputInstrument" className="text-sm font-medium">
                Output Instrument <span aria-hidden="true">*</span>
              </label>
              <Input
                id="outputInstrument"
                value={formData.outputInstrument}
                onChange={handleChange('outputInstrument')}
                placeholder={accountCurrency}
                aria-describedby={errors.outputInstrument ? 'outputInstrument-error' : undefined}
                aria-required="true"
              />
              <p className="text-xs text-muted-foreground">
                Must match the account currency: {accountCurrency}
              </p>
              {errors.outputInstrument && (
                <p id="outputInstrument-error" className="text-sm text-destructive">
                  {errors.outputInstrument}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="parameters" className="text-sm font-medium">
                Parameters (optional)
              </label>
              <Input
                id="parameters"
                value={formData.parameters}
                onChange={handleChange('parameters')}
                placeholder='e.g. {"source": "ecb"}'
                aria-describedby={errors.parameters ? 'parameters-error' : undefined}
              />
              <p className="text-xs text-muted-foreground">
                Optional JSON configuration for the valuation method
              </p>
              {errors.parameters && (
                <p id="parameters-error" className="text-sm text-destructive">
                  {errors.parameters}
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
            form="create-valuation-feature-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Creating...' : 'Add Valuation Feature'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
