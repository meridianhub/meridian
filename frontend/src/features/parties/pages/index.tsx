import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { TimeDisplay } from '@/shared/time-display'
import { StatusBadge } from '@/shared/status-badge'
import { PageHeader, PageShell } from '@/shared'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { usePartiesTable } from '../hooks'
import { RegisterPartyDialog } from './dialogs/register-party-dialog'
import { usePageTitle } from '@/hooks/use-page-title'
import { RegisterPartyTypeDialog } from './dialogs/register-party-type-dialog'

export interface Party {
  partyId: string
  legalName: string
  partyType: string
  status: string
  externalReference?: string
  createdAt?: { seconds: bigint | number; nanos?: number }
}

function formatPartyStatus(value: string): string {
  return value.replace(/^PARTY_STATUS_/, '')
}

function formatPartyType(value: string): string {
  return value.replace(/^PARTY_TYPE_/, '')
}

export function PartiesPage() {
  usePageTitle('Parties')
  const navigate = useNavigate()
  const { queryKey, queryFn, tenantSlug } = usePartiesTable()
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
      cell: ({ row }) => (
        <span className="text-sm">{formatPartyType(row.original.partyType)}</span>
      ),
    },
    {
      accessorKey: 'status',
      header: 'Status',
      cell: ({ row }) => <StatusBadge status={formatPartyStatus(row.original.status)} />,
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

  const handleRowClick = (party: Party) => {
    navigate(`/parties/${party.partyId}`)
  }

  if (!tenantSlug) return null

  return (
    <PageShell>
      <PageHeader
        title="Parties"
        description="Manage parties, their demographics, and linked accounts."
        actions={
          <>
            <Button onClick={() => setRegisterOpen(true)}>Register Party</Button>
            <Button variant="outline" onClick={() => setAddPartyTypeOpen(true)}>
              Add Party Type
            </Button>
          </>
        }
      />

      <RegisterPartyDialog open={registerOpen} onOpenChange={setRegisterOpen} />
      <RegisterPartyTypeDialog
        open={addPartyTypeOpen}
        onOpenChange={setAddPartyTypeOpen}
      />

      <Card className="p-6">
        <DataTable
          queryKey={queryKey}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          filters={filters}
          onRowClick={handleRowClick}
        />
      </Card>
    </PageShell>
  )
}
