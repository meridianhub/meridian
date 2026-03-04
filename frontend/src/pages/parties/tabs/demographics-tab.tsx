import * as React from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TimeDisplay } from '@/shared/time-display'
import { useForm } from 'react-hook-form'

interface DemographicsTabProps {
  partyId: string
}

interface DemographicsFormData {
  socioEconomicData: string
  employmentHistory: string
}

export function DemographicsTab({ partyId }: DemographicsTabProps) {
  const clients = useClients()
  const queryClient = useQueryClient()
  const [isEditing, setIsEditing] = React.useState(false)
  const { register, handleSubmit, reset } = useForm<DemographicsFormData>()

  const { data: demographics, isLoading } = useQuery({
    queryKey: ['party', partyId, 'demographics'],
    queryFn: async () => {
      return await clients.party.retrieveDemographics({ partyId })
    },
  })

  const updateMutation = useMutation({
    mutationFn: async (data: DemographicsFormData) => {
      return await clients.party.updateDemographics({
        partyId,
        socioEconomicData: data.socioEconomicData,
        employmentHistory: data.employmentHistory,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['party', partyId, 'demographics'] })
      setIsEditing(false)
    },
  })

  React.useEffect(() => {
    if (demographics) {
      reset({
        socioEconomicData: demographics.socioEconomicData ?? '',
        employmentHistory: demographics.employmentHistory ?? '',
      })
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
        <div className="grid gap-6">
          <div>
            <label htmlFor="socioEconomicData" className="text-sm font-medium">Socio-Economic Data</label>
            <Input
              id="socioEconomicData"
              {...register('socioEconomicData')}
              placeholder="Socio-economic classification"
              className="mt-1"
            />
          </div>

          <div>
            <label htmlFor="employmentHistory" className="text-sm font-medium">Employment History</label>
            <Input
              id="employmentHistory"
              {...register('employmentHistory')}
              placeholder="Employment information"
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
              reset({
                socioEconomicData: demographics?.socioEconomicData ?? '',
                employmentHistory: demographics?.employmentHistory ?? '',
              })
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
      <div className="grid gap-4">
        {[
          { label: 'Socio-Economic Data', value: demographics.socioEconomicData },
          { label: 'Employment History', value: demographics.employmentHistory },
          { label: 'Last Updated', value: demographics.updatedAt ? <TimeDisplay timestamp={demographics.updatedAt} /> : undefined },
        ].map(({ label, value }) => (
          <div key={label}>
            <span className="text-sm font-medium text-muted-foreground">{label}</span>
            <p className="mt-1 text-sm">{value || '—'}</p>
          </div>
        ))}
      </div>

      <Button onClick={() => setIsEditing(true)}>Edit Demographics</Button>
    </div>
  )
}
