import * as React from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'
import { Button } from '@/components/ui/button'
import { usePartyAssociations } from '../../hooks'
import { RegisterAssociationsDialog } from '../dialogs/register-associations-dialog'

interface AssociationsTabProps {
  partyId: string
}

export function AssociationsTab({ partyId }: AssociationsTabProps) {
  const [dialogOpen, setDialogOpen] = React.useState(false)

  const { isLoading } = usePartyAssociations(partyId)

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-1/3" />
      </div>
    )
  }

  return (
    <>
      <div className="flex justify-end mb-4">
        <Button onClick={() => setDialogOpen(true)}>Add Association</Button>
      </div>
      <EmptyState title="Associations" description="No associations information available." />
      <RegisterAssociationsDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        partyId={partyId}
      />
    </>
  )
}
