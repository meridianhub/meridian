import type { ReactNode } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '@/contexts/auth-context'

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
