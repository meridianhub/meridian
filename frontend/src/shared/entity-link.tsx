import { Link } from 'react-router-dom'

export type EntityType =
  | 'account'
  | 'current-account'
  | 'party'
  | 'internal-account'
  | 'payment'
  | 'booking-log'
  | 'position'

function entityPath(type: EntityType, id: string): string {
  const encodedId = encodeURIComponent(id)
  switch (type) {
    case 'account':
      return `/accounts/${encodedId}`
    case 'current-account':
      return `/accounts/${encodedId}`
    case 'party':
      return `/parties/${encodedId}`
    case 'internal-account':
      return `/internal-accounts/${encodedId}`
    case 'payment':
      return `/payments/${encodedId}`
    case 'booking-log':
      return `/ledger/${encodedId}`
    case 'position':
      return `/positions/${encodedId}`
  }
}

export interface EntityLinkProps {
  type: EntityType
  id: string
  /** Display text; defaults to the id */
  label?: string
  className?: string
}

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

export function EntityLink({ type, id, label, className }: EntityLinkProps) {
  if (!id) return null
  return (
    <Link
      to={entityPath(type, id)}
      className={className ?? 'text-blue-600 hover:underline dark:text-blue-400'}
    >
      {label ?? id}
    </Link>
  )
}
