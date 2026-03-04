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

    // Fall back to the first enabled feature if the configured default is not in the enabled list.
    // When no features are enabled at all, defaultFeature is an empty string (no valid default).
    const configuredDefault = config.features?.defaultFeature ?? 'dashboard'
    const defaultFeature = enabledSet.has(configuredDefault)
      ? configuredDefault
      : (enabledFeatures[0] ?? '')

    return {
      isFeatureEnabled: (feature: string) => enabledSet.has(feature),
      enabledFeatures,
      defaultFeature,
    }
  }, [tenantConfig])
}
