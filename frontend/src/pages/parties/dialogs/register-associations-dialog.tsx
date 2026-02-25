import * as React from 'react'
import { Code } from '@connectrpc/connect'
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
import { useApiClients } from '@/api/context'
import { handleConnectError } from '@/lib/error-handling'
import { tenantKeys } from '@/lib/query-keys'
import { useTenantSlug } from '@/hooks/use-tenant-context'

// RelationshipType enum values (from proto meridian.party.v1)
const RELATIONSHIP_TYPE_ENUM: Record<string, number> = {
  RELATIONSHIP_TYPE_SPOUSE: 1,
  RELATIONSHIP_TYPE_DEPENDENT: 2,
  RELATIONSHIP_TYPE_BUSINESS_PARTNER: 3,
  RELATIONSHIP_TYPE_GUARANTOR: 4,
  RELATIONSHIP_TYPE_BENEFICIAL_OWNER: 5,
  RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT: 6,
  RELATIONSHIP_TYPE_SYNDICATE_HOST: 7,
}

const RELATIONSHIP_TYPE_LABELS: Record<string, string> = {
  RELATIONSHIP_TYPE_SPOUSE: 'Spouse',
  RELATIONSHIP_TYPE_DEPENDENT: 'Dependent',
  RELATIONSHIP_TYPE_BUSINESS_PARTNER: 'Business Partner',
  RELATIONSHIP_TYPE_GUARANTOR: 'Guarantor',
  RELATIONSHIP_TYPE_BENEFICIAL_OWNER: 'Beneficial Owner',
  RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT: 'Syndicate Participant',
  RELATIONSHIP_TYPE_SYNDICATE_HOST: 'Syndicate Host',
}

interface Party {
  partyId: string
  displayName: string
  legalName?: string
}

interface FormData {
  relatedPartyId: string
  relatedPartyName: string
  relationshipType: string
  effectiveFrom: string
  effectiveTo: string
}

interface FormErrors {
  relatedPartyId?: string
  relationshipType?: string
  effectiveTo?: string
  general?: string
}

export interface RegisterAssociationsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  partyId: string
}

const initialFormData: FormData = {
  relatedPartyId: '',
  relatedPartyName: '',
  relationshipType: '',
  effectiveFrom: '',
  effectiveTo: '',
}

function useDebounce<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = React.useState(value)
  React.useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(timer)
  }, [value, delay])
  return debounced
}

function parseLocalDateToTimestamp(dateStr: string): { seconds: bigint; nanos: number } | undefined {
  if (!dateStr) return undefined
  const date = new Date(`${dateStr}T00:00:00Z`)
  if (isNaN(date.getTime())) return undefined
  return { seconds: BigInt(Math.floor(date.getTime() / 1000)), nanos: 0 }
}

export function RegisterAssociationsDialog({
  open,
  onOpenChange,
  partyId,
}: RegisterAssociationsDialogProps) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()
  const queryClient = useQueryClient()

  const [formData, setFormData] = React.useState<FormData>(initialFormData)
  const [errors, setErrors] = React.useState<FormErrors>({})
  const [searchInput, setSearchInput] = React.useState('')
  const [showDropdown, setShowDropdown] = React.useState(false)

  const debouncedSearch = useDebounce(searchInput, 300)

  React.useEffect(() => {
    if (!open) {
      setFormData(initialFormData)
      setErrors({})
      setSearchInput('')
      setShowDropdown(false)
    }
  }, [open])

  const { data: partySearchData } = useQuery({
    queryKey: ['party-search', debouncedSearch],
    queryFn: () => clients.party.listParties({ searchQuery: debouncedSearch, pageSize: 20 }),
    enabled: debouncedSearch.length >= 2,
  })

  const searchResults: Party[] = partySearchData?.parties ?? []

  const mutation = useMutation({
    mutationFn: async () => {
      const relationshipTypeValue = RELATIONSHIP_TYPE_ENUM[formData.relationshipType] ?? 0
      return clients.party.registerAssociations({
        partyId,
        relatedPartyId: formData.relatedPartyId,
        relationshipType: relationshipTypeValue,
        effectiveFrom: parseLocalDateToTimestamp(formData.effectiveFrom),
        effectiveTo: parseLocalDateToTimestamp(formData.effectiveTo),
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: tenantKeys.partyAssociations(tenantSlug ?? '', partyId),
      })
      onOpenChange(false)
    },
    onError: (err) => {
      const result = handleConnectError(err)
      if (result.code === Code.InvalidArgument && Object.keys(result.fieldErrors).length > 0) {
        const fieldMap: FormErrors = {}
        for (const [field, msg] of Object.entries(result.fieldErrors)) {
          if (field === 'related_party_id') fieldMap.relatedPartyId = msg
          else if (field === 'relationship_type') fieldMap.relationshipType = msg
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

    if (!formData.relatedPartyId) {
      newErrors.relatedPartyId = 'Related party is required'
    }

    if (!formData.relationshipType) {
      newErrors.relationshipType = 'Relationship type is required'
    }

    if (formData.effectiveFrom && formData.effectiveTo) {
      if (formData.effectiveTo < formData.effectiveFrom) {
        newErrors.effectiveTo = 'Effective to must be after effective from'
      }
    }

    setErrors(newErrors)
    return Object.keys(newErrors).length === 0
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    mutation.mutate()
  }

  function handleSearchChange(e: React.ChangeEvent<HTMLInputElement>) {
    const value = e.target.value
    setSearchInput(value)
    setShowDropdown(true)
    // Clear selected party if user edits the input
    if (formData.relatedPartyId) {
      setFormData((prev) => ({ ...prev, relatedPartyId: '', relatedPartyName: '' }))
    }
    if (errors.relatedPartyId) {
      setErrors((prev) => ({ ...prev, relatedPartyId: undefined }))
    }
  }

  function handleSelectParty(party: Party) {
    setFormData((prev) => ({
      ...prev,
      relatedPartyId: party.partyId,
      relatedPartyName: party.displayName,
    }))
    setSearchInput(party.displayName)
    setShowDropdown(false)
  }

  function handleFieldChange(field: keyof Pick<FormData, 'relationshipType' | 'effectiveFrom' | 'effectiveTo'>) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field as keyof FormErrors]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  const showResults = showDropdown && debouncedSearch.length >= 2 && !formData.relatedPartyId

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Association</DialogTitle>
          <DialogDescription>
            Create a new party association for this party.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="register-associations-form">
          <div className="space-y-4 py-2">
            {errors.general && (
              <div
                role="alert"
                className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {errors.general}
              </div>
            )}

            <div className="relative space-y-1">
              <label htmlFor="relatedParty" className="text-sm font-medium">
                Related Party <span aria-hidden="true">*</span>
              </label>
              <Input
                id="relatedParty"
                value={searchInput}
                onChange={handleSearchChange}
                onFocus={() => setShowDropdown(true)}
                placeholder="Search by name..."
                autoComplete="off"
                aria-describedby={errors.relatedPartyId ? 'relatedParty-error' : undefined}
                aria-required="true"
                aria-label="Related Party"
              />
              {errors.relatedPartyId && (
                <p id="relatedParty-error" className="text-sm text-destructive">
                  {errors.relatedPartyId}
                </p>
              )}
              {showResults && searchResults.length > 0 && (
                <ul
                  role="listbox"
                  aria-label="Party search results"
                  className="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-md border border-input bg-popover shadow-md"
                >
                  {searchResults.map((party) => (
                    <li
                      key={party.partyId}
                      role="option"
                      aria-selected={false}
                      className="cursor-pointer px-3 py-2 text-sm hover:bg-accent hover:text-accent-foreground"
                      onMouseDown={(e) => {
                        e.preventDefault()
                        handleSelectParty(party)
                      }}
                    >
                      <span className="font-medium">{party.displayName}</span>
                      <span className="ml-2 text-xs text-muted-foreground">{party.partyId}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="relationshipType" className="text-sm font-medium">
                Relationship Type <span aria-hidden="true">*</span>
              </label>
              <select
                id="relationshipType"
                value={formData.relationshipType}
                onChange={handleFieldChange('relationshipType')}
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                aria-describedby={errors.relationshipType ? 'relationshipType-error' : undefined}
                aria-required="true"
              >
                <option value="">Select relationship type</option>
                {Object.entries(RELATIONSHIP_TYPE_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
              {errors.relationshipType && (
                <p id="relationshipType-error" className="text-sm text-destructive">
                  {errors.relationshipType}
                </p>
              )}
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <label htmlFor="effectiveFrom" className="text-sm font-medium">
                  Effective From
                </label>
                <Input
                  id="effectiveFrom"
                  type="date"
                  value={formData.effectiveFrom}
                  onChange={handleFieldChange('effectiveFrom')}
                  aria-describedby={undefined}
                />
              </div>

              <div className="space-y-1">
                <label htmlFor="effectiveTo" className="text-sm font-medium">
                  Effective To
                </label>
                <Input
                  id="effectiveTo"
                  type="date"
                  value={formData.effectiveTo}
                  onChange={handleFieldChange('effectiveTo')}
                  aria-describedby={errors.effectiveTo ? 'effectiveTo-error' : undefined}
                />
                {errors.effectiveTo && (
                  <p id="effectiveTo-error" className="text-sm text-destructive">
                    {errors.effectiveTo}
                  </p>
                )}
              </div>
            </div>
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="register-associations-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Adding...' : 'Add Association'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
