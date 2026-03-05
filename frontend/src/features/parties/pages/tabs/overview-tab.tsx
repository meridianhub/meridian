import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { TimeDisplay } from '@/shared/time-display'
import { StatusBadge } from '@/shared/status-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'

const PARTY_TYPE_LABELS: Record<number, string> = {
  0: 'UNSPECIFIED',
  1: 'INDIVIDUAL',
  2: 'ORGANIZATION',
  3: 'GOVERNMENT',
}

const PARTY_STATUS_LABELS: Record<number, string> = {
  0: 'UNSPECIFIED',
  1: 'ACTIVE',
  2: 'SUSPENDED',
  3: 'CLOSED',
}

function formatPartyType(value: unknown): string {
  if (typeof value === 'string') return value.replace(/^PARTY_TYPE_/, '')
  if (typeof value === 'number') return PARTY_TYPE_LABELS[value] ?? String(value)
  return String(value ?? '')
}

function formatPartyStatus(value: unknown): string {
  if (typeof value === 'string') return value.replace(/^PARTY_STATUS_/, '')
  if (typeof value === 'number') return PARTY_STATUS_LABELS[value] ?? String(value)
  return String(value ?? '')
}

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
    { label: 'Type', value: formatPartyType(party.partyType) },
    { label: 'Status', value: <StatusBadge status={formatPartyStatus(party.status)} /> },
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
