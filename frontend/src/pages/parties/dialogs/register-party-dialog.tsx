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

// Maps PartyTypeDefinition.party_type strings to PartyType enum values.
// PARTY_TYPE_PERSON = 1, PARTY_TYPE_ORGANIZATION = 2
const PARTY_TYPE_ENUM: Record<string, number> = {
  PERSON: 1,
  ORGANIZATION: 2,
}

interface FormData {
  displayName: string
  partyType: string
  legalName: string
}

interface FormErrors {
  displayName?: string
  partyType?: string
  legalName?: string
  general?: string
}

export interface RegisterPartyDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const initialFormData: FormData = {
  displayName: '',
  partyType: '',
  legalName: '',
}

export function RegisterPartyDialog({ open, onOpenChange }: RegisterPartyDialogProps) {
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

  const { data: partyTypesData, isLoading: partyTypesLoading } = useQuery({
    queryKey: tenantKeys.partyTypes(tenantSlug ?? ''),
    queryFn: () => clients.party.listPartyTypes({}),
    enabled: open,
  })

  const partyTypeDefinitions = partyTypesData?.partyTypeDefinitions ?? []
  const noPartyTypes = !partyTypesLoading && partyTypeDefinitions.length === 0

  const mutation = useMutation({
    mutationFn: async () => {
      const partyTypeValue = PARTY_TYPE_ENUM[formData.partyType] ?? 0
      return clients.party.registerParty({
        partyType: partyTypeValue,
        legalName: formData.legalName.trim() || formData.displayName.trim(),
        displayName: formData.displayName.trim(),
      })
    },
    onSuccess: (data) => {
      const partyId = data.party?.partyId ?? ''
      queryClient.invalidateQueries({ queryKey: tenantKeys.parties(tenantSlug ?? '') })
      onOpenChange(false)
      if (partyId) {
        navigate(`/parties/${partyId}`)
      }
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'display_name') fieldMap.displayName = msg
          else if (field === 'party_type') fieldMap.partyType = msg
          else if (field === 'legal_name') fieldMap.legalName = msg
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

    if (!formData.displayName.trim()) {
      newErrors.displayName = 'Display name is required'
    } else if (formData.displayName.trim().length > 255) {
      newErrors.displayName = 'Display name must be 255 characters or fewer'
    }

    if (!formData.partyType) {
      newErrors.partyType = 'Party type is required'
    }

    if (formData.legalName.trim() && formData.legalName.trim().length > 255) {
      newErrors.legalName = 'Legal name must be 255 characters or fewer'
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
    return (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
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
          <DialogTitle>Register Party</DialogTitle>
          <DialogDescription>
            Create a new party in the reference data directory.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="register-party-form">
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
              <label htmlFor="displayName" className="text-sm font-medium">
                Display Name <span aria-hidden="true">*</span>
              </label>
              <Input
                id="displayName"
                value={formData.displayName}
                onChange={handleChange('displayName')}
                placeholder="Acme Corp"
                aria-describedby={errors.displayName ? 'displayName-error' : undefined}
                aria-required="true"
              />
              {errors.displayName && (
                <p id="displayName-error" className="text-sm text-destructive">
                  {errors.displayName}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="partyType" className="text-sm font-medium">
                Party Type <span aria-hidden="true">*</span>
              </label>
              {partyTypesLoading ? (
                <select
                  id="partyType"
                  disabled
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-busy="true"
                >
                  <option value="">Loading party types...</option>
                </select>
              ) : noPartyTypes ? (
                <div>
                  <select
                    id="partyType"
                    disabled
                    aria-disabled="true"
                    className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                    aria-describedby="partyType-hint"
                  >
                    <option value="">No party types configured</option>
                  </select>
                  <p id="partyType-hint" className="mt-1 text-sm text-muted-foreground">
                    No party types have been configured for this tenant. Please configure party types first.
                  </p>
                </div>
              ) : (
                <select
                  id="partyType"
                  value={formData.partyType}
                  onChange={handleChange('partyType')}
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                  aria-describedby={errors.partyType ? 'partyType-error' : undefined}
                  aria-required="true"
                >
                  <option value="">Select a party type</option>
                  {partyTypeDefinitions.map((def) => (
                    <option key={def.id} value={def.partyType}>
                      {def.partyType}
                    </option>
                  ))}
                </select>
              )}
              {errors.partyType && (
                <p id="partyType-error" className="text-sm text-destructive">
                  {errors.partyType}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="legalName" className="text-sm font-medium">
                Legal Name
              </label>
              <Input
                id="legalName"
                value={formData.legalName}
                onChange={handleChange('legalName')}
                placeholder="Acme Corporation Ltd"
                aria-describedby={errors.legalName ? 'legalName-error' : undefined}
              />
              {errors.legalName && (
                <p id="legalName-error" className="text-sm text-destructive">
                  {errors.legalName}
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
            form="register-party-form"
            disabled={mutation.isPending || partyTypesLoading || noPartyTypes}
          >
            {mutation.isPending ? 'Registering...' : 'Register Party'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
