import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'

interface ReferencesTabProps {
  partyId: string
}

export function ReferencesTab({ partyId }: ReferencesTabProps) {
  const clients = useClients()

  const { isLoading } = useQuery({
    queryKey: ['party', partyId, 'references'],
    queryFn: async () => {
      return await clients.party.retrieveReference({ partyId })
    },
  })

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-1/3" />
      </div>
    )
  }

  return <EmptyState title="References" description="No references information available." />
}
