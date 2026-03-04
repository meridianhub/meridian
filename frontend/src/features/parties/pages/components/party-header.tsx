import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { StatusBadge } from '@/shared/status-badge'
import { Skeleton } from '@/components/ui/skeleton'

function partyStatusLabel(status: unknown): string {
  if (typeof status === 'string') return status
  const map: Record<number, string> = {
    0: 'UNSPECIFIED',
    1: 'PARTY_STATUS_ACTIVE',
    2: 'PARTY_STATUS_RESTRICTED',
    3: 'PARTY_STATUS_SUSPENDED',
    4: 'PARTY_STATUS_TERMINATED',
  }
  return map[status as number] ?? 'UNKNOWN'
}

function partyTypeLabel(partyType: unknown): string {
  if (typeof partyType === 'string') return partyType
  const map: Record<number, string> = {
    0: 'UNSPECIFIED',
    1: 'Person',
    2: 'Organization',
  }
  return map[partyType as number] ?? 'Unknown'
}

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
              {partyTypeLabel(party.partyType)}
            </span>
            <StatusBadge status={partyStatusLabel(party.status)} />
          </div>
        </div>
      </div>
    </div>
  )
}
