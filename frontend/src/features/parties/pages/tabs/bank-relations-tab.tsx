import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'

interface BankRelationsTabProps {
  partyId: string
}

export function BankRelationsTab({ partyId }: BankRelationsTabProps) {
  const clients = useClients()

  const { isLoading } = useQuery({
    queryKey: ['party', partyId, 'bank-relations'],
    queryFn: async () => {
      return await clients.party.retrieveBankRelations({ partyId })
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

  return <EmptyState title="Bank Relations" description="No bank relations information available." />
}
