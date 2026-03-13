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
  success: 'bg-success-muted text-success-foreground border-success/30',
  warning: 'bg-warning-muted text-warning-foreground border-warning/30',
  error: 'bg-destructive/10 text-destructive border-destructive/30',
  info: 'bg-info-muted text-info-foreground border-info/30',
  neutral: 'bg-muted text-muted-foreground border-border',
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
        className="inline-flex h-5 w-16 animate-pulse rounded-full bg-muted"
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
