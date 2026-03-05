import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { platformKeys } from '@/lib/query-keys'
import { TenantStatus } from '@/api/gen/meridian/tenant/v1/tenant_pb'

export function useTenant(tenantId: string) {
  const { tenant } = useApiClients()

  return useQuery({
    queryKey: platformKeys.tenant(tenantId),
    queryFn: async () => {
      const response = await tenant.retrieveTenant({ tenantId })
      return response.tenant
    },
    enabled: !!tenantId,
  })
}

const PROVISIONING_STATUSES = new Set([
  TenantStatus.PROVISIONING,
  TenantStatus.PROVISIONING_PENDING,
])

export function useTenantProvisioningStatus(tenantId: string, tenantStatus?: TenantStatus) {
  const { tenant } = useApiClients()
  const isProvisioning = tenantStatus !== undefined && PROVISIONING_STATUSES.has(tenantStatus)

  return useQuery({
    queryKey: platformKeys.tenantProvisioningStatus(tenantId),
    queryFn: async () => {
      const response = await tenant.getTenantProvisioningStatus({ tenantId })
      return response
    },
    enabled: !!tenantId,
    refetchInterval: isProvisioning ? 2000 : false,
  })
}

export function useUpdateTenantStatus(tenantId: string) {
  const { tenant } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (status: TenantStatus) => {
      const response = await tenant.updateTenantStatus({ tenantId, status })
      return response.tenant
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: platformKeys.tenant(tenantId) })
      void queryClient.invalidateQueries({
        queryKey: platformKeys.tenantProvisioningStatus(tenantId),
      })
      void queryClient.invalidateQueries({ queryKey: platformKeys.tenants() })
    },
  })
}

export function useInitiateTenant() {
  const { tenant } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: {
      tenantId: string
      displayName: string
      settlementAsset: string
      slug?: string
      subdomain?: string
    }) => {
      const response = await tenant.initiateTenant({
        tenantId: request.tenantId,
        displayName: request.displayName,
        settlementAsset: request.settlementAsset,
        slug: request.slug ?? '',
        subdomain: request.subdomain ?? '',
      })
      return response.tenant
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: platformKeys.tenants() })
    },
  })
}
