import { Link } from 'react-router-dom'
import { isUuid } from '@/lib/utils'

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
      // UUID → current account, human-readable code → internal account
      return isUuid(id)
        ? `/accounts/${encodedId}`
        : `/internal-accounts/${encodedId}`
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
