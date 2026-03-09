import { cn } from '@/lib/utils'

type StatusVariant = 'success' | 'warning' | 'error' | 'info' | 'neutral'

const STATUS_MAP: Record<string, StatusVariant> = {
  // Account statuses
  ACTIVE: 'success',
  FROZEN: 'warning',
  CLOSED: 'neutral',
  SUSPENDED: 'error',
  // Payment order statuses
  INITIATED: 'info',
  RESERVED: 'info',
  EXECUTING: 'warning',
  COMPLETED: 'success',
  FAILED: 'error',
  CANCELLED: 'neutral',
  REVERSED: 'neutral',
  // Saga statuses
  DRAFT: 'neutral',
  DEPRECATED: 'warning',
  // Reconciliation statuses
  RUNNING: 'warning',
  // Tenant statuses
  PROVISIONING: 'info',
  PROVISIONING_PENDING: 'info',
  PROVISIONING_FAILED: 'error',
  DEPROVISIONED: 'neutral',
  // Identity statuses
  PENDING_INVITE: 'info',
  LOCKED: 'error',
  // Manifest apply statuses
  APPLIED: 'success',
  ROLLED_BACK: 'warning',
  // Position quality ladder
  ESTIMATE: 'warning',
  COEFFICIENT: 'info',
  ACTUAL: 'success',
  REVISED: 'info',
}

const VARIANT_STYLES: Record<StatusVariant, string> = {
  success: 'bg-green-100 text-green-800 border-green-200',
  warning: 'bg-yellow-100 text-yellow-800 border-yellow-200',
  error: 'bg-red-100 text-red-800 border-red-200',
  info: 'bg-blue-100 text-blue-800 border-blue-200',
  neutral: 'bg-gray-100 text-gray-800 border-gray-200',
}

interface StatusBadgeProps {
  status: string
  loading?: boolean
}

export function StatusBadge({ status, loading }: StatusBadgeProps) {
  if (loading) {
    return (
      <span
        data-testid="status-badge-skeleton"
        className="inline-flex h-5 w-16 animate-pulse rounded-full bg-gray-200"
      />
    )
  }

  const statusStr = typeof status === 'string' ? status : String(status)
  const variant = STATUS_MAP[statusStr] ?? 'neutral'
  const displayText = statusStr.replace(/_/g, ' ')

  return (
    <span
      className={cn(
        'inline-flex items-center justify-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        VARIANT_STYLES[variant],
      )}
    >
      {displayText}
    </span>
  )
}
