import { useQuery } from '@tanstack/react-query'

export interface AuthProvider {
  id: string
  type: 'password' | 'oidc'
  displayName: string
  iconUrl?: string
}

interface ProvidersResponse {
  providers: AuthProvider[]
}

async function fetchProviders(): Promise<AuthProvider[]> {
  const response = await fetch('/api/auth/providers')
  if (!response.ok) {
    throw new Error(`Failed to fetch providers: ${response.status}`)
  }
  const data = (await response.json()) as ProvidersResponse
  return data.providers
}

/**
 * Fetches available authentication providers from the backend.
 * Gracefully falls back to empty array on error (password-only mode).
 */
export function useAuthProviders() {
  return useQuery<AuthProvider[]>({
    queryKey: ['auth', 'providers'],
    queryFn: fetchProviders,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
}
