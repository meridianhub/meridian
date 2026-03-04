import { useMemo } from 'react'
import { useTenantContext } from '@/contexts/tenant-context'
import {
  ALL_FEATURES,
  DEFAULT_UI_CONFIG,
  type FeatureId,
} from '@/lib/tenant-ui-config'

export { ALL_FEATURES }

export interface TenantFeaturesResult {
  isFeatureEnabled: (feature: string) => boolean
  enabledFeatures: readonly FeatureId[]
  defaultFeature: string
}

export function useTenantFeatures(): TenantFeaturesResult {
  const { tenantConfig } = useTenantContext()

  return useMemo(() => {
    const config = tenantConfig ?? DEFAULT_UI_CONFIG
    const enabledFeatures = config.features?.enabled ?? [...ALL_FEATURES]
    const enabledSet = new Set<string>(enabledFeatures)

    // Fall back to the first enabled feature if the configured default is not in the enabled list
    const configuredDefault = config.features?.defaultFeature ?? 'dashboard'
    const defaultFeature = enabledSet.has(configuredDefault)
      ? configuredDefault
      : (enabledFeatures[0] ?? 'dashboard')

    return {
      isFeatureEnabled: (feature: string) => enabledSet.has(feature),
      enabledFeatures,
      defaultFeature,
    }
  }, [tenantConfig])
}
