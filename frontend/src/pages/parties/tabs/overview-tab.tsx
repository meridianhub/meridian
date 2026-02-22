import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { TimeDisplay } from '@/components/shared/time-display'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'

interface OverviewTabProps {
  partyId: string
}

interface PartyOverview {
  partyId: string
  name: string
  partyType: string
  status: string
  externalReference?: string
  createdAt?: { seconds: bigint | number; nanos?: number }
  updatedAt?: { seconds: bigint | number; nanos?: number }
  verificationStatus?: string
}

export function OverviewTab({ partyId }: OverviewTabProps) {
  const clients = useClients()

  const { data: party, isLoading } = useQuery({
    queryKey: ['party', partyId, 'overview'],
    queryFn: async () => {
      const response = await clients.party.getParticipant({ partyId })
      return response as PartyOverview
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
    { label: 'Name', value: party.name },
    { label: 'Type', value: party.partyType },
    { label: 'Status', value: party.status },
    { label: 'External Reference', value: party.externalReference || '—' },
    { label: 'Verification Status', value: party.verificationStatus || '—' },
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
