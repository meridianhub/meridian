import * as React from 'react'
import { Code } from '@connectrpc/connect'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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
import { tenantKeys } from '@/lib/query-keys'
import { useTenantSlug } from '@/hooks/use-tenant-context'

// Behavior classes that are considered internal (excludes CUSTOMER = 1)
const INTERNAL_BEHAVIOR_CLASSES = [2, 3, 4, 5, 6, 7, 8, 9]

// Account code pattern: uppercase letters, digits, underscores, hyphens
const ACCOUNT_CODE_PATTERN = /^[A-Z0-9_-]+$/

interface FormData {
  accountName: string
  accountCode: string
  accountType: string
  instrumentCode: string
  description: string
}

interface FormErrors {
  accountName?: string
  accountCode?: string
  accountType?: string
  instrumentCode?: string
  description?: string
  general?: string
}

export interface CreateInternalAccountDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const initialFormData: FormData = {
  accountName: '',
  accountCode: '',
  accountType: '',
  instrumentCode: '',
  description: '',
}

export function CreateInternalAccountDialog({ open, onOpenChange }: CreateInternalAccountDialogProps) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const [formData, setFormData] = React.useState<FormData>(initialFormData)
  const [errors, setErrors] = React.useState<FormErrors>({})

  React.useEffect(() => {
    if (!open) {
      setFormData(initialFormData)
      setErrors({})
    }
  }, [open])

  const { data: accountTypesData, isLoading: accountTypesLoading } = useQuery({
    queryKey: ['account-types-internal'],
    queryFn: async () => {
      const res = await clients.accountTypeRegistry.listActive({})
      return res.definitions?.filter((d) => INTERNAL_BEHAVIOR_CLASSES.includes(d.behaviorClass)) ?? []
    },
    enabled: open,
  })

  const { data: instrumentsData, isLoading: instrumentsLoading } = useQuery({
    queryKey: ['instruments-active'],
    queryFn: async () => {
      const res = await clients.referenceData.listInstruments({ statusFilter: 2 })
      return res.instruments ?? []
    },
    enabled: open,
  })

  const accountTypes = accountTypesData ?? []
  const instruments = instrumentsData ?? []

  const mutation = useMutation({
    mutationFn: async () => {
      return clients.internalBankAccount.initiateInternalBankAccount({
        name: formData.accountName.trim(),
        accountCode: formData.accountCode.trim(),
        productTypeCode: formData.accountType,
        instrumentCode: formData.instrumentCode,
        description: formData.description.trim(),
      })
    },
    onSuccess: (data) => {
      const accountId = (data as { accountId?: string }).accountId ?? ''
      queryClient.invalidateQueries({ queryKey: tenantKeys.internalAccounts(tenantSlug ?? '') })
      onOpenChange(false)
      if (accountId) {
        navigate(`/internal-accounts/${accountId}`)
      }
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'name') fieldMap.accountName = msg
          else if (field === 'account_code') fieldMap.accountCode = msg
          else if (field === 'product_type_code') fieldMap.accountType = msg
          else if (field === 'instrument_code') fieldMap.instrumentCode = msg
          else if (field === 'description') fieldMap.description = msg
          else fieldMap.general = msg
        }
        setErrors(fieldMap)
      } else {
        setErrors({ general: result.message })
      }
    },
  })

  function validate(): boolean {
    const newErrors: FormErrors = {}

    if (!formData.accountName.trim()) {
      newErrors.accountName = 'Account name is required'
    } else if (formData.accountName.trim().length > 255) {
      newErrors.accountName = 'Account name must be 255 characters or fewer'
    }

    if (!formData.accountCode.trim()) {
      newErrors.accountCode = 'Account code is required'
    } else if (formData.accountCode.trim().length > 50) {
      newErrors.accountCode = 'Account code must be 50 characters or fewer'
    } else if (!ACCOUNT_CODE_PATTERN.test(formData.accountCode.trim())) {
      newErrors.accountCode = 'Account code must contain only uppercase letters, digits, underscores, or hyphens'
    }

    if (!formData.accountType) {
      newErrors.accountType = 'Account type is required'
    }

    if (!formData.instrumentCode) {
      newErrors.instrumentCode = 'Instrument is required'
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
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New Internal Account</DialogTitle>
          <DialogDescription>
            Create a new internal bank account such as a clearing, nostro, vostro, or suspense account.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-internal-account-form">
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
              <label htmlFor="accountName" className="text-sm font-medium">
                Account Name <span aria-hidden="true">*</span>
              </label>
              <Input
                id="accountName"
                value={formData.accountName}
                onChange={handleChange('accountName')}
                placeholder="GBP Clearing Account"
                aria-describedby={errors.accountName ? 'accountName-error' : undefined}
                aria-required="true"
              />
              {errors.accountName && (
                <p id="accountName-error" className="text-sm text-destructive">
                  {errors.accountName}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="accountCode" className="text-sm font-medium">
                Account Code <span aria-hidden="true">*</span>
              </label>
              <Input
                id="accountCode"
                value={formData.accountCode}
                onChange={handleChange('accountCode')}
                placeholder="CLR-GBP-001"
                aria-describedby={errors.accountCode ? 'accountCode-error' : undefined}
                aria-required="true"
              />
              {errors.accountCode && (
                <p id="accountCode-error" className="text-sm text-destructive">
                  {errors.accountCode}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="accountType" className="text-sm font-medium">
                Account Type <span aria-hidden="true">*</span>
              </label>
              {accountTypesLoading ? (
                <select
                  id="accountType"
                  disabled
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-busy="true"
                >
                  <option value="">Loading account types...</option>
                </select>
              ) : accountTypes.length === 0 ? (
                <div>
                  <select
                    id="accountType"
                    value={formData.accountType}
                    onChange={handleChange('accountType')}
                    className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                    aria-describedby={errors.accountType ? 'accountType-error' : 'accountType-hint'}
                  >
                    <option value="">No internal account types configured</option>
                  </select>
                  <p id="accountType-hint" className="mt-1 text-sm text-muted-foreground">
                    No internal account types have been configured for this tenant. Please configure account types first.
                  </p>
                </div>
              ) : (
                <select
                  id="accountType"
                  value={formData.accountType}
                  onChange={handleChange('accountType')}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-describedby={errors.accountType ? 'accountType-error' : undefined}
                  aria-required="true"
                >
                  <option value="">Select an account type</option>
                  {accountTypes.map((def) => (
                    <option key={def.id} value={def.code}>
                      {def.displayName || def.code}
                    </option>
                  ))}
                </select>
              )}
              {errors.accountType && (
                <p id="accountType-error" className="text-sm text-destructive">
                  {errors.accountType}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="instrumentCode" className="text-sm font-medium">
                Instrument <span aria-hidden="true">*</span>
              </label>
              {instrumentsLoading ? (
                <select
                  id="instrumentCode"
                  disabled
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-busy="true"
                >
                  <option value="">Loading instruments...</option>
                </select>
              ) : (
                <select
                  id="instrumentCode"
                  value={formData.instrumentCode}
                  onChange={handleChange('instrumentCode')}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-describedby={errors.instrumentCode ? 'instrumentCode-error' : undefined}
                  aria-required="true"
                >
                  <option value="">Select an instrument</option>
                  {instruments.map((inst) => (
                    <option key={inst.code} value={inst.code}>
                      {inst.code}{inst.displayName ? ` — ${inst.displayName}` : ''}
                    </option>
                  ))}
                </select>
              )}
              {errors.instrumentCode && (
                <p id="instrumentCode-error" className="text-sm text-destructive">
                  {errors.instrumentCode}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="description" className="text-sm font-medium">
                Description
              </label>
              <textarea
                id="description"
                value={formData.description}
                onChange={handleChange('description')}
                placeholder="Optional description of this account's purpose"
                rows={3}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-none"
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
            form="create-internal-account-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Creating...' : 'Create Account'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
