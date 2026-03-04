import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { Plus } from 'lucide-react'
import { useApiClients } from '@/api/context'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { platformKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { Tenant } from '@/api/gen/meridian/tenant/v1/tenant_pb'
import { TenantStatus } from '@/api/gen/meridian/tenant/v1/tenant_pb'
import { useInitiateTenant } from '@/hooks/use-tenant'

const STATUS_LABEL: Record<number, string> = {
  [TenantStatus.ACTIVE]: 'ACTIVE',
  [TenantStatus.SUSPENDED]: 'SUSPENDED',
  [TenantStatus.DEPROVISIONED]: 'DEPROVISIONED',
  [TenantStatus.PROVISIONING]: 'PROVISIONING',
  [TenantStatus.PROVISIONING_FAILED]: 'PROVISIONING_FAILED',
  [TenantStatus.PROVISIONING_PENDING]: 'PROVISIONING_PENDING',
}

const columns: ColumnDef<Tenant>[] = [
  {
    accessorKey: 'tenantId',
    header: 'Tenant ID',
  },
  {
    accessorKey: 'displayName',
    header: 'Display Name',
  },
  {
    accessorKey: 'slug',
    header: 'Slug',
  },
  {
    accessorKey: 'settlementAsset',
    header: 'Settlement Asset',
  },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => {
      const statusLabel = STATUS_LABEL[row.original.status] ?? 'UNKNOWN'
      return <StatusBadge status={statusLabel} />
    },
  },
  {
    accessorKey: 'createdAt',
    header: 'Created',
    cell: ({ row }) => <TimeDisplay timestamp={row.original.createdAt} />,
  },
]

interface InitiateTenantFormData {
  tenantId: string
  displayName: string
  settlementAsset: string
  slug: string
}

interface InitiateTenantDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

function InitiateTenantDialog({ open, onOpenChange, onSuccess }: InitiateTenantDialogProps) {
  const initiateTenant = useInitiateTenant()
  const [formData, setFormData] = React.useState<InitiateTenantFormData>({
    tenantId: '',
    displayName: '',
    settlementAsset: '',
    slug: '',
  })
  const [errors, setErrors] = React.useState<Partial<InitiateTenantFormData>>({})

  React.useEffect(() => {
    if (!open) {
      setFormData({ tenantId: '', displayName: '', settlementAsset: '', slug: '' })
      setErrors({})
    }
  }, [open])

  function validate(): boolean {
    const newErrors: Partial<InitiateTenantFormData> = {}
    if (!formData.tenantId.trim()) {
      newErrors.tenantId = 'Tenant ID is required'
    }
    if (!formData.displayName.trim()) {
      newErrors.displayName = 'Display Name is required'
    }
    if (!formData.settlementAsset.trim()) {
      newErrors.settlementAsset = 'Settlement Asset is required'
    }
    setErrors(newErrors)
    return Object.keys(newErrors).length === 0
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return

    try {
      await initiateTenant.mutateAsync({
        tenantId: formData.tenantId,
        displayName: formData.displayName,
        settlementAsset: formData.settlementAsset,
        slug: formData.slug || undefined,
      })
      onSuccess()
      onOpenChange(false)
      setFormData({ tenantId: '', displayName: '', settlementAsset: '', slug: '' })
      setErrors({})
    } catch {
      // error handled by mutation state
    }
  }

  function handleChange(field: keyof InitiateTenantFormData) {
    return (e: React.ChangeEvent<HTMLInputElement>) => {
      setFormData((prev) => ({ ...prev, [field]: e.target.value }))
      if (errors[field]) {
        setErrors((prev) => ({ ...prev, [field]: undefined }))
      }
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Initiate Tenant</DialogTitle>
        </DialogHeader>
        <form onSubmit={(e) => void handleSubmit(e)} id="initiate-tenant-form">
          <div className="space-y-4 py-2">
            <div className="space-y-1">
              <label htmlFor="tenantId" className="text-sm font-medium">
                Tenant ID
              </label>
              <Input
                id="tenantId"
                value={formData.tenantId}
                onChange={handleChange('tenantId')}
                placeholder="acme_corp"
                aria-describedby={errors.tenantId ? 'tenantId-error' : undefined}
              />
              {errors.tenantId && (
                <p id="tenantId-error" className="text-sm text-destructive">
                  {errors.tenantId}
                </p>
              )}
            </div>
            <div className="space-y-1">
              <label htmlFor="displayName" className="text-sm font-medium">
                Display Name
              </label>
              <Input
                id="displayName"
                value={formData.displayName}
                onChange={handleChange('displayName')}
                placeholder="ACME Corporation"
                aria-describedby={errors.displayName ? 'displayName-error' : undefined}
              />
              {errors.displayName && (
                <p id="displayName-error" className="text-sm text-destructive">
                  {errors.displayName}
                </p>
              )}
            </div>
            <div className="space-y-1">
              <label htmlFor="settlementAsset" className="text-sm font-medium">
                Settlement Asset
              </label>
              <Input
                id="settlementAsset"
                value={formData.settlementAsset}
                onChange={handleChange('settlementAsset')}
                placeholder="GBP"
                aria-describedby={errors.settlementAsset ? 'settlementAsset-error' : undefined}
              />
              {errors.settlementAsset && (
                <p id="settlementAsset-error" className="text-sm text-destructive">
                  {errors.settlementAsset}
                </p>
              )}
            </div>
            <div className="space-y-1">
              <label htmlFor="slug" className="text-sm font-medium">
                Slug (optional)
              </label>
              <Input
                id="slug"
                value={formData.slug}
                onChange={handleChange('slug')}
                placeholder="acme-corp"
              />
            </div>
          </div>
        </form>
        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="initiate-tenant-form"
            disabled={initiateTenant.isPending}
          >
            {initiateTenant.isPending ? 'Creating...' : 'Initiate Tenant'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function TenantsPage() {
  const { tenant } = useApiClients()
  const navigate = useNavigate()
  const [dialogOpen, setDialogOpen] = React.useState(false)
  const [tableKey, setTableKey] = React.useState(0)

  async function fetchTenants(params: DataTableQueryParams): Promise<DataTableResult<Tenant>> {
    const response = await tenant.listTenants({
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
    })
    return {
      items: response.tenants ?? [],
      nextPageToken: response.nextPageToken || undefined,
    }
  }

  function handleRowClick(row: Tenant) {
    void navigate(`/tenants/${row.tenantId}`)
  }

  function handleTenantCreated() {
    // Force refresh the table by bumping the key
    setTableKey((k) => k + 1)
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Tenant Management</h1>
        <Button onClick={() => setDialogOpen(true)}>
          <Plus className="mr-2 size-4" />
          New Tenant
        </Button>
      </div>

      <DataTable<Tenant>
        key={tableKey}
        queryKey={platformKeys.tenants()}
        queryFn={fetchTenants}
        columns={columns}
        pageSize={25}
        onRowClick={handleRowClick}
      />

      <InitiateTenantDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onSuccess={handleTenantCreated}
      />
    </div>
  )
}
