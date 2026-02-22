import { useAuth } from '@/contexts/auth-context'

export function useIsAuthenticated(): boolean {
  return useAuth().isAuthenticated
}

export function useUserClaims() {
  return useAuth().claims
}

export function useUserLens(): 'platform' | 'tenant' {
  return useAuth().lens
}

export function useHasRole(role: string): boolean {
  const claims = useAuth().claims
  if (!claims) return false
  return claims.roles.includes(role)
}

export function useHasScope(scope: string): boolean {
  const claims = useAuth().claims
  if (!claims) return false
  return claims.scopes.includes(scope)
}
