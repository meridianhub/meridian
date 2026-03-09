import type { EntityType } from './entity-link'

/**
 * Resolves a proto AccountServiceDomain enum value to the correct EntityType.
 * 0 = UNSPECIFIED -> 'account' (default, falls back to current account route)
 * 1 = CURRENT_ACCOUNT -> 'current-account'
 * 2 = INTERNAL_ACCOUNT -> 'internal-account'
 */
export function accountEntityType(serviceDomain?: number): EntityType {
  if (serviceDomain === 2) return 'internal-account'
  if (serviceDomain === 1) return 'current-account'
  return 'account'
}
