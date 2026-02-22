import * as React from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useForm } from 'react-hook-form'

interface DemographicsTabProps {
  partyId: string
}

interface Demographics {
  partyId: string
  email?: string
  phoneNumber?: string
  businessName?: string
  businessRegistration?: string
  legalName?: string
  dateOfBirth?: { seconds: bigint | number; nanos?: number }
  nationality?: string
  taxId?: string
  website?: string
  streetAddress?: string
  city?: string
  state?: string
  postalCode?: string
  country?: string
}

interface DemographicsFormData {
  email?: string
  phoneNumber?: string
  businessName?: string
  businessRegistration?: string
  legalName?: string
  nationality?: string
  taxId?: string
  website?: string
  streetAddress?: string
  city?: string
  state?: string
  postalCode?: string
  country?: string
}

export function DemographicsTab({ partyId }: DemographicsTabProps) {
  const clients = useClients()
  const queryClient = useQueryClient()
  const [isEditing, setIsEditing] = React.useState(false)
  const { register, handleSubmit, reset, formState: { errors } } = useForm<DemographicsFormData>()

  const { data: demographics, isLoading } = useQuery({
    queryKey: ['party', partyId, 'demographics'],
    queryFn: async () => {
      const response = await clients.party.getParticipant({ partyId })
      return response as Demographics
    },
  })

  const updateMutation = useMutation({
    mutationFn: async (data: DemographicsFormData) => {
      return await clients.party.updateParticipant({
        partyId,
        ...data,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['party', partyId, 'demographics'] })
      setIsEditing(false)
    },
  })

  React.useEffect(() => {
    if (demographics) {
      reset(demographics)
    }
  }, [demographics, reset])

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-1/3" />
      </div>
    )
  }

  if (!demographics) {
    return <EmptyState title="No data" description="Demographics information not found." />
  }

  if (isEditing) {
    return (
      <form onSubmit={handleSubmit((data) => void updateMutation.mutateAsync(data))} className="space-y-6">
        <div className="grid gap-6 md:grid-cols-2">
          <div>
            <label className="text-sm font-medium">Email</label>
            <Input
              {...register('email')}
              placeholder="Email"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Phone Number</label>
            <Input
              {...register('phoneNumber')}
              placeholder="Phone Number"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Business Name</label>
            <Input
              {...register('businessName')}
              placeholder="Business Name"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Business Registration</label>
            <Input
              {...register('businessRegistration')}
              placeholder="Business Registration"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Legal Name</label>
            <Input
              {...register('legalName')}
              placeholder="Legal Name"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Nationality</label>
            <Input
              {...register('nationality')}
              placeholder="Nationality"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Tax ID</label>
            <Input
              {...register('taxId')}
              placeholder="Tax ID"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Website</label>
            <Input
              {...register('website')}
              placeholder="Website"
              className="mt-1"
            />
          </div>

          <div className="md:col-span-2">
            <label className="text-sm font-medium">Street Address</label>
            <Input
              {...register('streetAddress')}
              placeholder="Street Address"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">City</label>
            <Input
              {...register('city')}
              placeholder="City"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">State/Province</label>
            <Input
              {...register('state')}
              placeholder="State/Province"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Postal Code</label>
            <Input
              {...register('postalCode')}
              placeholder="Postal Code"
              className="mt-1"
            />
          </div>

          <div>
            <label className="text-sm font-medium">Country</label>
            <Input
              {...register('country')}
              placeholder="Country"
              className="mt-1"
            />
          </div>
        </div>

        <div className="flex gap-2">
          <Button type="submit" disabled={updateMutation.isPending}>
            Save Changes
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setIsEditing(false)
              reset()
            }}
          >
            Cancel
          </Button>
        </div>
      </form>
    )
  }

  return (
    <div className="space-y-6">
      <div className="grid gap-4 md:grid-cols-2">
        {[
          { label: 'Email', value: demographics.email },
          { label: 'Phone Number', value: demographics.phoneNumber },
          { label: 'Business Name', value: demographics.businessName },
          { label: 'Business Registration', value: demographics.businessRegistration },
          { label: 'Legal Name', value: demographics.legalName },
          { label: 'Nationality', value: demographics.nationality },
          { label: 'Tax ID', value: demographics.taxId },
          { label: 'Website', value: demographics.website },
          { label: 'Street Address', value: demographics.streetAddress, fullWidth: true },
          { label: 'City', value: demographics.city },
          { label: 'State/Province', value: demographics.state },
          { label: 'Postal Code', value: demographics.postalCode },
          { label: 'Country', value: demographics.country },
        ].map(({ label, value, fullWidth }) => (
          <div key={label} className={fullWidth ? 'md:col-span-2' : ''}>
            <span className="text-sm font-medium text-muted-foreground">{label}</span>
            <p className="mt-1 text-sm">{value || '—'}</p>
          </div>
        ))}
      </div>

      <Button onClick={() => setIsEditing(true)}>Edit Demographics</Button>
    </div>
  )
}
