import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '@/contexts/auth-context'
import { useTenantContext } from '@/contexts/tenant-context'
import { apiConfig, isOnTenantSubdomain } from '@/api/config'

interface ProtectedRouteProps {
  children: ReactNode
}

/**
 * Redirects unauthenticated users to /login.
 * Wrap any route that requires authentication.
 */
export function ProtectedRoute({ children }: ProtectedRouteProps) {
  const { isAuthenticated } = useAuth()
  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

interface PlatformOnlyRouteProps {
  children: ReactNode
}

/**
 * Redirects non-platform users to /.
 * Wrap any route that requires platform-admin or super-admin lens.
 */
export function PlatformOnlyRoute({ children }: PlatformOnlyRouteProps) {
  const { lens } = useAuth()
  if (lens !== 'platform') {
    return <Navigate to="/" replace />
  }
  return <>{children}</>
}

interface AdminOnlyRouteProps {
  children: ReactNode
}

const ADMIN_ROLES = ['admin', 'tenant-admin', 'super-admin', 'platform-admin', 'tenant-owner']

/**
 * Redirects users without admin-level roles to /.
 * Wrap routes that require admin access (e.g., identity management).
 */
export function AdminOnlyRoute({ children }: AdminOnlyRouteProps) {
  const { claims, lens } = useAuth()
  const roles = claims?.roles ?? []
  const hasAdminRole = lens === 'platform' || roles.some((r) => ADMIN_ROLES.includes(r))
  if (!hasAdminRole) {
    return <Navigate to="/" replace />
  }
  return <>{children}</>
}

/** Routes that live on the root domain (no tenant subdomain needed). */
const PLATFORM_PATHS = ['/login', '/tenants', '/platform', '/users']

function isPlatformPath(pathname: string): boolean {
  return PLATFORM_PATHS.some((p) => pathname === p || pathname.startsWith(p + '/'))
}

function isLocalDev(): boolean {
  const hostname = window.location.hostname
  return hostname === 'localhost' || hostname === '127.0.0.1'
}

/**
 * Redirects to the tenant subdomain when the user is on the root domain
 * but navigating to a tenant-scoped route with a selected tenant.
 *
 * Platform routes (/tenants, /platform, /users, /login) stay on root.
 * Local dev is never redirected (no subdomain support).
 */
export function TenantSubdomainEnforcer({ children }: { children: ReactNode }) {
  const { pathname, search, hash } = useLocation()
  const { tenantSlug } = useTenantContext()

  // Skip in local dev — subdomains don't work on localhost
  if (isLocalDev()) {
    return <>{children}</>
  }

  // Already on a tenant subdomain — no redirect needed
  if (isOnTenantSubdomain()) {
    return <>{children}</>
  }

  // Platform routes stay on root domain
  if (isPlatformPath(pathname)) {
    return <>{children}</>
  }

  // Tenant-scoped route on root domain with a tenant selected → redirect
  if (tenantSlug) {
    const parsed = new URL(apiConfig.baseUrl)
    const target = new URL(window.location.href)
    target.hostname = `${tenantSlug}.${parsed.hostname}`
    target.pathname = pathname
    target.search = search
    target.hash = hash
    window.location.href = target.toString()
    return null
  }

  // No tenant selected — show content (DevTenantAutoSelector may still be loading)
  return <>{children}</>
}
