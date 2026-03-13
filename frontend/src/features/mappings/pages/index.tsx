import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { TimeDisplay } from '@/shared/time-display'
import { StatusBadge } from '@/shared/status-badge'
import { PageShell } from '@/shared/page-shell'
import { PageHeader } from '@/shared/page-header'
import { useApiClients } from '@/api/context'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { usePageTitle } from '@/hooks/use-page-title'
import { CreateMappingDialog } from './dialogs/create-mapping-dialog'

export interface MappingDefinition {
  id: string
  name: string
  targetService: string
  targetRpc: string
  version: number
  status: string
  createdAt?: { seconds: bigint | number; nanos?: number }
  updatedAt?: { seconds: bigint | number; nanos?: number }
}

interface ListMappingsParams {
  pageToken?: string
  pageSize: number
  filters?: Record<string, string>
}

interface ListMappingsResult {
  items: MappingDefinition[]
  nextPageToken?: string
}

// Map string enum names to proto numeric values for ListMappingsRequest.status
const STATUS_MAP: Record<string, 0 | 1 | 2 | 3> = {
  MAPPING_STATUS_DRAFT: 1,
  MAPPING_STATUS_ACTIVE: 2,
  MAPPING_STATUS_DEPRECATED: 3,
}

export function MappingsPage() {
  usePageTitle('Gateway Mappings')
  const navigate = useNavigate()
  const clients = useApiClients()
  const [createDialogOpen, setCreateDialogOpen] = React.useState(false)

  const columns: ColumnDef<MappingDefinition>[] = [
    {
      accessorKey: 'name',
      header: 'Name',
      cell: ({ row }) => (
        <span className="font-medium">{row.original.name}</span>
      ),
    },
    {
      accessorKey: 'targetService',
      header: 'Target Service',
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.original.targetService}
        </span>
      ),
    },
    {
      accessorKey: 'targetRpc',
      header: 'Target RPC',
      cell: ({ row }) => (
        <span className="font-mono text-xs">{row.original.targetRpc}</span>
      ),
    },
    {
      accessorKey: 'version',
      header: 'Version',
      cell: ({ row }) => `v${row.original.version}`,
    },
    {
      accessorKey: 'status',
      header: 'Status',
      cell: ({ row }) => {
        const raw = row.original.status
        // Strip the MAPPING_STATUS_ prefix for display
        const display = raw.replace(/^MAPPING_STATUS_/, '')
        return <StatusBadge status={display} />
      },
    },
    {
      accessorKey: 'updatedAt',
      header: 'Updated',
      cell: ({ row }) => <TimeDisplay timestamp={row.original.updatedAt} />,
    },
  ]

  const filters = [
    {
      field: 'status',
      label: 'Status',
      type: 'select' as const,
      options: [
        { label: 'Draft', value: 'MAPPING_STATUS_DRAFT' },
        { label: 'Active', value: 'MAPPING_STATUS_ACTIVE' },
        { label: 'Deprecated', value: 'MAPPING_STATUS_DEPRECATED' },
      ],
    },
  ]

  const queryFn = async (params: ListMappingsParams): Promise<ListMappingsResult> => {
    const response = await clients.mapping.listMappings({
      pageToken: params.pageToken,
      pageSize: params.pageSize,
      status: params.filters?.status ? STATUS_MAP[params.filters.status] : undefined,
    })

    return {
      items: (response.mappings ?? []) as MappingDefinition[],
      nextPageToken: response.nextPageToken,
    }
  }

  const handleRowClick = (mapping: MappingDefinition) => {
    navigate(`/gateway-mappings/${mapping.id}`)
  }

  const handleCreateSuccess = (mappingId: string) => {
    if (!mappingId) return
    navigate(`/gateway-mappings/${mappingId}`)
  }

  return (
    <PageShell>
      <PageHeader
        title="Gateway Mappings"
        description="Manage field correspondence mappings between external JSON payloads and internal Meridian services."
        actions={<Button onClick={() => setCreateDialogOpen(true)}>Create Mapping</Button>}
      />

      <CreateMappingDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
        onSuccess={handleCreateSuccess}
      />

      <Card className="p-6">
        <DataTable
          queryKey={['mappings']}
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
