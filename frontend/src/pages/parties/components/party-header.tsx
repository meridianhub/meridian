import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { StatusBadge } from '@/components/shared/status-badge'
import { Skeleton } from '@/components/ui/skeleton'

interface PartyHeaderProps {
  partyId: string
}

export function PartyHeader({ partyId }: PartyHeaderProps) {
  const clients = useClients()

  const { data: party, isLoading } = useQuery({
    queryKey: ['party', partyId],
    queryFn: async () => {
      const response = await clients.party.retrieveParty({ partyId })
      return response.party
    },
  })

  if (isLoading) {
    return (
      <div className="p-6 space-y-4">
        <Skeleton className="h-8 w-1/3" />
        <Skeleton className="h-4 w-1/4" />
      </div>
    )
  }

  if (!party) {
    return <div className="p-6 text-destructive">Party not found</div>
  }

  return (
    <div className="p-6 border-b">
      <div className="flex items-start justify-between">
        <div className="space-y-2">
          <h2 className="text-2xl font-bold">{party.legalName}</h2>
          <div className="flex items-center gap-3">
            <span className="text-sm text-muted-foreground">
              {party.partyType}
            </span>
            <StatusBadge status={party.status} />
          </div>
        </div>
      </div>
    </div>
  )
}
