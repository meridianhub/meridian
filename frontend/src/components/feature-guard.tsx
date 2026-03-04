import { Navigate } from 'react-router-dom'
import { useTenantFeatures } from '@/hooks/use-tenant-features'
import type { FeatureId } from '@/lib/tenant-ui-config'

interface FeatureGuardProps {
  feature: FeatureId
  children: React.ReactNode
  fallback?: string
}

/**
 * Redirects navigation to pages requiring a disabled feature.
 * This is a UX guard only — backend authorization is enforced separately.
 */
export function FeatureGuard({ feature, children, fallback = '/' }: FeatureGuardProps) {
  const { isFeatureEnabled } = useTenantFeatures()
  if (!isFeatureEnabled(feature)) {
    return <Navigate to={fallback} replace />
  }
  return <>{children}</>
}
