import { useParams } from 'react-router-dom'
import { CheckCircle2, Circle, Loader2, XCircle } from 'lucide-react'
import { Breadcrumbs } from '@/components/shared'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useTenant, useTenantProvisioningStatus, useUpdateTenantStatus } from '@/hooks/use-tenant'
import {
  TenantStatus,
  ServiceProvisioningStatus_Status,
} from '@/api/gen/meridian/tenant/v1/tenant_pb'
import type { ServiceProvisioningStatus } from '@/api/gen/meridian/tenant/v1/tenant_pb'
import { cn } from '@/lib/utils'

const TENANT_STATUS_LABEL: Record<number, string> = {
  [TenantStatus.ACTIVE]: 'ACTIVE',
  [TenantStatus.SUSPENDED]: 'SUSPENDED',
  [TenantStatus.DEPROVISIONED]: 'DEPROVISIONED',
  [TenantStatus.PROVISIONING]: 'PROVISIONING',
  [TenantStatus.PROVISIONING_FAILED]: 'PROVISIONING_FAILED',
  [TenantStatus.PROVISIONING_PENDING]: 'PROVISIONING_PENDING',
}

const SERVICE_STATUS_LABEL: Record<number, string> = {
  [ServiceProvisioningStatus_Status.UNSPECIFIED]: 'Unknown',
  [ServiceProvisioningStatus_Status.PENDING]: 'Pending',
  [ServiceProvisioningStatus_Status.IN_PROGRESS]: 'In Progress',
  [ServiceProvisioningStatus_Status.COMPLETED]: 'Completed',
  [ServiceProvisioningStatus_Status.FAILED]: 'Failed',
}

function ServiceStatusIcon({ status }: { status: ServiceProvisioningStatus_Status }) {
  switch (status) {
    case ServiceProvisioningStatus_Status.COMPLETED:
      return <CheckCircle2 className="size-4 text-green-600" aria-label="Completed" />
    case ServiceProvisioningStatus_Status.IN_PROGRESS:
      return <Loader2 className="size-4 animate-spin text-blue-600" aria-label="In Progress" />
    case ServiceProvisioningStatus_Status.FAILED:
      return <XCircle className="size-4 text-red-600" aria-label="Failed" />
    default:
      return <Circle className="size-4 text-gray-400" aria-label="Pending" />
  }
}

interface ProvisioningStatusGridProps {
  services: ServiceProvisioningStatus[]
}

function ProvisioningStatusGrid({ services }: ProvisioningStatusGridProps) {
  if (services.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">No service provisioning data available.</p>
    )
  }

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {services.map((svc) => (
        <div
          key={svc.serviceName}
          className={cn(
            'flex items-start gap-3 rounded-md border p-3',
            svc.status === ServiceProvisioningStatus_Status.FAILED && 'border-red-200 bg-red-50',
            svc.status === ServiceProvisioningStatus_Status.IN_PROGRESS && 'border-blue-200 bg-blue-50',
            svc.status === ServiceProvisioningStatus_Status.COMPLETED && 'border-green-200 bg-green-50',
          )}
        >
          <ServiceStatusIcon status={svc.status} />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium">{svc.serviceName}</p>
            <p className="text-xs text-muted-foreground">
              {SERVICE_STATUS_LABEL[svc.status] ?? 'Unknown'}
            </p>
            {svc.migrationVersion && (
              <p className="text-xs text-muted-foreground font-mono">{svc.migrationVersion}</p>
            )}
            {svc.errorMessage && (
              <p className="mt-1 text-xs text-red-700">{svc.errorMessage}</p>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

export function TenantDetailPage() {
  const { tenantId } = useParams<{ tenantId: string }>()
  const { data: tenant, isLoading: tenantLoading } = useTenant(tenantId ?? '')
  const { data: provisioningStatus } = useTenantProvisioningStatus(
    tenantId ?? '',
    tenant?.status,
  )
  const updateStatus = useUpdateTenantStatus(tenantId ?? '')

  if (tenantLoading) {
    return (
      <div className="p-6">
        <div className="h-8 w-48 animate-pulse rounded bg-muted" />
      </div>
    )
  }

  if (!tenant) {
    return (
      <div className="p-6">
        <p className="text-muted-foreground">Tenant not found.</p>
      </div>
    )
  }

  const statusLabel = TENANT_STATUS_LABEL[tenant.status] ?? 'UNKNOWN'
  const isActive = tenant.status === TenantStatus.ACTIVE
  const isSuspended = tenant.status === TenantStatus.SUSPENDED

  function handleSuspend() {
    void updateStatus.mutateAsync(TenantStatus.SUSPENDED)
  }

  function handleActivate() {
    void updateStatus.mutateAsync(TenantStatus.ACTIVE)
  }

  function handleDeprovision() {
    void updateStatus.mutateAsync(TenantStatus.DEPROVISIONED)
  }

  return (
    <div className="p-6 space-y-6">
      {/* Breadcrumb navigation */}
      <Breadcrumbs
        items={[
          { label: 'Tenants', href: '/tenants' },
          { label: tenant.displayName },
        ]}
      />

      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">{tenant.displayName}</h1>
          <p className="text-sm text-muted-foreground">{tenant.tenantId}</p>
        </div>
        <div className="flex items-center gap-2">
          <StatusBadge status={statusLabel} />
          {isActive && (
            <Button
              variant="outline"
              size="sm"
              onClick={handleSuspend}
              disabled={updateStatus.isPending}
            >
              Suspend
            </Button>
          )}
          {isSuspended && (
            <Button
              variant="outline"
              size="sm"
              onClick={handleActivate}
              disabled={updateStatus.isPending}
            >
              Activate
            </Button>
          )}
          {(isActive || isSuspended) && (
            <Button
              variant="destructive"
              size="sm"
              onClick={handleDeprovision}
              disabled={updateStatus.isPending}
            >
              Deprovision
            </Button>
          )}
        </div>
      </div>

      {/* Details Card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Tenant Details</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
            <div>
              <dt className="text-muted-foreground">Tenant ID</dt>
              <dd className="font-mono">{tenant.tenantId}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Display Name</dt>
              <dd>{tenant.displayName}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Slug</dt>
              <dd className="font-mono">{tenant.slug}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Settlement Asset</dt>
              <dd className="font-mono">{tenant.settlementAsset}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Status</dt>
              <dd>
                <StatusBadge status={statusLabel} />
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Created</dt>
              <dd>
                <TimeDisplay timestamp={tenant.createdAt} />
              </dd>
            </div>
            {tenant.subdomain && (
              <div>
                <dt className="text-muted-foreground">Subdomain</dt>
                <dd className="font-mono">{tenant.subdomain}</dd>
              </div>
            )}
            {tenant.partyId && (
              <div>
                <dt className="text-muted-foreground">Party ID</dt>
                <dd className="font-mono">{tenant.partyId}</dd>
              </div>
            )}
            <div>
              <dt className="text-muted-foreground">Version</dt>
              <dd>{tenant.version}</dd>
            </div>
          </dl>
          {tenant.errorMessage && (
            <div className="mt-4 rounded-md border border-red-200 bg-red-50 p-3">
              <p className="text-sm font-medium text-red-800">Error</p>
              <p className="text-sm text-red-700">{tenant.errorMessage}</p>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Provisioning Status Card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Provisioning Status</CardTitle>
        </CardHeader>
        <CardContent>
          {provisioningStatus ? (
            <ProvisioningStatusGrid services={provisioningStatus.services ?? []} />
          ) : (
            <p className="text-sm text-muted-foreground">Loading provisioning status...</p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
