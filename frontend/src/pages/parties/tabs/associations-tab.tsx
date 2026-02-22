import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'

interface AssociationsTabProps {
  partyId: string
}

export function AssociationsTab({ partyId }: AssociationsTabProps) {
  const clients = useClients()

  const { isLoading } = useQuery({
    queryKey: ['party', partyId, 'associations'],
    queryFn: async () => {
      return await clients.party.getAssociations({ partyId })
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

  return <EmptyState title="Associations" description="No associations information available." />
}
