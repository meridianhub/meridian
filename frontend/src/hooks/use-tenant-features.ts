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

  const config = tenantConfig ?? DEFAULT_UI_CONFIG
  const enabledFeatures = config.features?.enabled ?? [...ALL_FEATURES]
  const defaultFeature = config.features?.defaultFeature ?? 'dashboard'

  const enabledSet = new Set<string>(enabledFeatures)

  return {
    isFeatureEnabled: (feature: string) => enabledSet.has(feature),
    enabledFeatures,
    defaultFeature,
  }
}
