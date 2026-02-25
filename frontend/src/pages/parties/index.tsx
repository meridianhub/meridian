import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/components/shared/data-table'
import { TimeDisplay } from '@/components/shared/time-display'
import { StatusBadge } from '@/components/shared/status-badge'
import { useClients } from '@/api/context'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { tenantKeys } from '@/lib/query-keys'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { RegisterPartyDialog } from './dialogs/register-party-dialog'
import { RegisterPartyTypeDialog } from './dialogs/register-party-type-dialog'

export interface Party {
  partyId: string
  legalName: string
  partyType: string
  status: string
  externalReference?: string
  createdAt?: { seconds: bigint | number; nanos?: number }
}

interface ListPartiesParams {
  pageToken?: string
  pageSize: number
  filters?: Record<string, string>
}

interface ListPartiesResult {
  items: Party[]
  nextPageToken?: string
}

export function PartiesPage() {
  const navigate = useNavigate()
  const clients = useClients()
  const tenantSlug = useTenantSlug()
  const [registerOpen, setRegisterOpen] = React.useState(false)
  const [addPartyTypeOpen, setAddPartyTypeOpen] = React.useState(false)

  const columns: ColumnDef<Party>[] = [
    {
      accessorKey: 'legalName',
      header: 'Name',
      cell: ({ row }) => row.original.legalName,
    },
    {
      accessorKey: 'partyType',
      header: 'Party Type',
      cell: ({ row }) => {
        const type = row.original.partyType
        return <span className="text-sm">{type}</span>
      },
    },
    {
      accessorKey: 'status',
      header: 'Status',
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'externalReference',
      header: 'External Ref',
      cell: ({ row }) => row.original.externalReference || '—',
    },
    {
      accessorKey: 'createdAt',
      header: 'Created',
      cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} />,
    },
  ]

  const filters = [
    {
      field: 'partyType',
      label: 'Party Type',
      type: 'select' as const,
      options: [
        { label: 'Person', value: 'PARTY_TYPE_PERSON' },
        { label: 'Organization', value: 'PARTY_TYPE_ORGANIZATION' },
      ],
    },
    {
      field: 'status',
      label: 'Status',
      type: 'select' as const,
      options: [
        { label: 'Active', value: 'PARTY_STATUS_ACTIVE' },
        { label: 'Restricted', value: 'PARTY_STATUS_RESTRICTED' },
        { label: 'Suspended', value: 'PARTY_STATUS_SUSPENDED' },
        { label: 'Terminated', value: 'PARTY_STATUS_TERMINATED' },
      ],
    },
    {
      field: 'searchQuery',
      label: 'Search',
      type: 'text' as const,
    },
  ]

  const queryFn = async (params: ListPartiesParams): Promise<ListPartiesResult> => {
    const response = await clients.party.listParties({
      pageToken: params.pageToken,
      pageSize: params.pageSize,
      searchQuery: params.filters?.searchQuery,
      partyType: params.filters?.partyType,
      status: params.filters?.status,
    })

    const parties: Party[] = response.parties.map((p: Party) => ({
      partyId: p.partyId,
      legalName: p.legalName,
      partyType: p.partyType,
      status: p.status,
      externalReference: p.externalReference,
      createdAt: p.createdAt,
    }))

    return {
      items: parties,
      nextPageToken: response.nextPageToken,
    }
  }

  const handleRowClick = (party: Party) => {
    navigate(`/parties/${party.partyId}`)
  }

  if (!tenantSlug) return null

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Parties</h1>
          <p className="mt-2 text-muted-foreground">
            Manage parties, their demographics, and linked accounts.
          </p>
        </div>
        <div className="flex gap-2">
          <Button onClick={() => setRegisterOpen(true)}>Register Party</Button>
          <Button variant="outline" onClick={() => setAddPartyTypeOpen(true)}>
            Add Party Type
          </Button>
        </div>
      </div>

      <RegisterPartyDialog open={registerOpen} onOpenChange={setRegisterOpen} />
      <RegisterPartyTypeDialog
        open={addPartyTypeOpen}
        onOpenChange={setAddPartyTypeOpen}
      />

      <Card className="p-6">
        <DataTable
          queryKey={tenantKeys.parties(tenantSlug)}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          filters={filters}
          onRowClick={handleRowClick}
        />
      </Card>
    </div>
  )
}
