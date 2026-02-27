import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { TimeDisplay } from '@/components/shared/time-display'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'

interface OverviewTabProps {
  partyId: string
}

export function OverviewTab({ partyId }: OverviewTabProps) {
  const clients = useClients()

  const { data: party, isLoading } = useQuery({
    queryKey: ['party', partyId, 'overview'],
    queryFn: async () => {
      const response = await clients.party.retrieveParty({ partyId })
      return response.party
    },
  })

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-1/3" />
      </div>
    )
  }

  if (!party) {
    return <EmptyState title="No data" description="Party information not found." />
  }

  const infoRows = [
    { label: 'Party ID', value: party.partyId },
    { label: 'Legal Name', value: party.legalName },
    { label: 'Display Name', value: party.displayName || '—' },
    { label: 'Type', value: party.partyType },
    { label: 'Status', value: party.status },
    { label: 'External Reference', value: party.externalReference || '—' },
    { label: 'Created', value: party.createdAt ? <TimeDisplay timestamp={party.createdAt} /> : '—' },
    { label: 'Updated', value: party.updatedAt ? <TimeDisplay timestamp={party.updatedAt} /> : '—' },
  ]

  return (
    <div className="space-y-4">
      <div className="grid gap-4">
        {infoRows.map(({ label, value }) => (
          <div key={label} className="grid grid-cols-3 gap-4 border-b pb-3 last:border-b-0">
            <span className="text-sm font-medium text-muted-foreground">{label}</span>
            <span className="col-span-2 text-sm">{value}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
