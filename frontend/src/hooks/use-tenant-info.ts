import { useQuery } from '@tanstack/react-query'

interface TenantInfoResponse {
  slug: string
  displayName: string
}

async function fetchTenantInfo(): Promise<TenantInfoResponse | null> {
  const response = await fetch('/api/tenant-info')
  if (response.status === 404) {
    // No valid tenant subdomain - bare domain or unknown tenant
    return null
  }
  if (!response.ok) {
    throw new Error(`Failed to fetch tenant info: ${response.status}`)
  }
  return (await response.json()) as TenantInfoResponse
}

/**
 * Fetches tenant info from the public /api/tenant-info endpoint.
 * Used on the login page (pre-authentication) to display the tenant name.
 * Returns null when on the bare domain or when the endpoint is unavailable.
 */
export function useTenantInfo() {
  const { data, isLoading } = useQuery({
    queryKey: ['tenant-info'],
    queryFn: fetchTenantInfo,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  return {
    displayName: data?.displayName ?? null,
    slug: data?.slug ?? null,
    isLoading,
  }
}
