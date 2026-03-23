import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'
import { Button } from '@/components/ui/button'
import { EntityLink } from '@/shared/entity-link'
import { StatusBadge } from '@/shared/status-badge'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import {
  RelationshipType,
  AssociationStatus,
  PartyType,
} from '@/api/gen/meridian/party/v1/party_pb'
import type { Association } from '@/api/gen/meridian/party/v1/party_pb'
import { usePartyAssociations } from '../../hooks'
import { RegisterAssociationsDialog } from '../dialogs/register-associations-dialog'

interface AssociationsTabProps {
  partyId: string
  /** Party type passed from the parent page to avoid a duplicate fetch */
  partyType?: number | string
}

const RELATIONSHIP_TYPE_LABELS: Record<number, string> = {
  [RelationshipType.UNSPECIFIED]: 'Unknown',
  [RelationshipType.SPOUSE]: 'Spouse',
  [RelationshipType.DEPENDENT]: 'Dependent',
  [RelationshipType.BUSINESS_PARTNER]: 'Business Partner',
  [RelationshipType.GUARANTOR]: 'Guarantor',
  [RelationshipType.BENEFICIAL_OWNER]: 'Beneficial Owner',
  [RelationshipType.SYNDICATE_PARTICIPANT]: 'Syndicate Participant',
  [RelationshipType.SYNDICATE_HOST]: 'Syndicate Host',
}

const ASSOCIATION_STATUS_LABELS: Record<number, string> = {
  [AssociationStatus.UNSPECIFIED]: 'UNKNOWN',
  [AssociationStatus.ACTIVE]: 'ACTIVE',
  [AssociationStatus.SUSPENDED]: 'SUSPENDED',
  [AssociationStatus.TERMINATED]: 'TERMINATED',
}

function relationshipTypeLabel(type: number | string): string {
  if (typeof type === 'string') return type
  return RELATIONSHIP_TYPE_LABELS[type] ?? String(type)
}

function associationStatusLabel(status: number | string): string {
  if (typeof status === 'string') return status
  return ASSOCIATION_STATUS_LABELS[status] ?? String(status)
}

function metadataSummary(metadata: Record<string, unknown> | undefined): string {
  if (!metadata) return '-'
  const keys = Object.keys(metadata)
  if (keys.length === 0) return '-'
  if (keys.length === 1) {
    const val = metadata[keys[0]]
    return `${keys[0]}: ${JSON.stringify(val)}`
  }
  return `${keys.length} fields`
}

interface AssociationTableProps {
  associations: Association[]
}

function AssociationTable({ associations }: AssociationTableProps) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-left text-muted-foreground">
            <th className="pb-2 pr-4 font-medium">Party</th>
            <th className="pb-2 pr-4 font-medium">Relationship</th>
            <th className="pb-2 pr-4 font-medium">Status</th>
            <th className="pb-2 font-medium">Metadata</th>
          </tr>
        </thead>
        <tbody>
          {associations.map((assoc) => (
            <tr key={assoc.associationId} className="hover:bg-muted/50">
              <td className="py-2 pr-4">
                <EntityLink type="party" id={assoc.relatedPartyId} />
              </td>
              <td className="py-2 pr-4">
                {relationshipTypeLabel(assoc.relationshipType as number)}
              </td>
              <td className="py-2 pr-4">
                <StatusBadge status={associationStatusLabel(assoc.status as number)} />
              </td>
              <td className="py-2 text-muted-foreground">
                {metadataSummary(assoc.metadata as Record<string, unknown> | undefined)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function AssociationsTab({ partyId, partyType }: AssociationsTabProps) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()
  const [dialogOpen, setDialogOpen] = React.useState(false)

  const isOrganization =
    partyType === PartyType.ORGANIZATION ||
    partyType === 'PARTY_TYPE_ORGANIZATION' ||
    partyType === 'ORGANIZATION'

  // For PERSON parties: retrieve forward associations (relationships registered by this party).
  // Disabled for org parties — orgs use listParticipants instead.
  const { data: associationsData, isLoading: isLoadingAssociations } = usePartyAssociations(
    isOrganization ? undefined : partyId,
  )

  // For ORGANIZATION parties: list participants (members of this org/syndicate)
  const { data: participantsData, isLoading: isLoadingParticipants } = useQuery({
    queryKey: [...tenantKeys.party(tenantSlug ?? '', partyId), 'participants'],
    queryFn: () => clients.party.listParticipants({ partyId }),
    enabled: Boolean(tenantSlug && partyId && isOrganization),
  })

  const isLoading = isOrganization ? isLoadingParticipants : isLoadingAssociations

  const associations: Association[] = isOrganization
    ? (participantsData?.participants ?? [])
    : (associationsData?.associations ?? [])

  const tableTitle = isOrganization ? 'Members' : 'Organizations'
  const emptyDescription = isOrganization
    ? 'No members registered for this organization.'
    : 'No associations information available.'

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
      {associations.length === 0 ? (
        <EmptyState title={tableTitle} description={emptyDescription} />
      ) : (
        <AssociationTable associations={associations} />
      )}
      <RegisterAssociationsDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        partyId={partyId}
      />
    </>
  )
}
