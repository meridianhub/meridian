import { cn } from '@/lib/utils'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { EmailDeliveryStatus } from '../api/types'

type EmailStatusVariant = 'muted' | 'info' | 'success' | 'error'

interface StatusConfig {
  variant: EmailStatusVariant
  label: string
}

const STATUS_CONFIG: Record<NonNullable<EmailDeliveryStatus['status']>, StatusConfig> = {
  PENDING: { variant: 'muted', label: 'Pending' },
  SENT: { variant: 'info', label: 'Sent' },
  DELIVERED: { variant: 'success', label: 'Delivered' },
  BOUNCED: { variant: 'error', label: 'Bounced' },
  DEAD_LETTER: { variant: 'error', label: 'Dead Letter' },
  CANCELLED: { variant: 'muted', label: 'Cancelled' },
}

const VARIANT_STYLES: Record<EmailStatusVariant, string> = {
  muted: 'bg-muted text-muted-foreground border-border',
  info: 'bg-info-muted text-info-foreground border-info/30',
  success: 'bg-success-muted text-success-foreground border-success/30',
  error: 'bg-destructive/10 text-destructive border-destructive/30',
}

interface EmailDeliveryStatusBadgeProps {
  status: EmailDeliveryStatus | undefined
  compact?: boolean
}

export function EmailDeliveryStatusBadge({ status, compact = false }: EmailDeliveryStatusBadgeProps) {
  if (!status) {
    return (
      <span className="inline-flex items-center justify-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap bg-muted text-muted-foreground border-border">
        No email
      </span>
    )
  }

  const config = STATUS_CONFIG[status.status] ?? { variant: 'muted' as const, label: status.status }
  const badgeEl = (
    <span
      className={cn(
        'inline-flex items-center justify-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        VARIANT_STYLES[config.variant],
      )}
    >
      {compact ? config.label.charAt(0) : config.label}
    </span>
  )

  const hasTooltipContent = status.sentAt || status.deliveredAt || status.bounceReason

  if (!hasTooltipContent) return badgeEl

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>{badgeEl}</TooltipTrigger>
        <TooltipContent>
          <div className="space-y-1 text-xs">
            {status.sentAt && (
              <div>
                <span className="font-medium">Sent: </span>
                {new Date(status.sentAt).toLocaleString()}
              </div>
            )}
            {status.deliveredAt && (
              <div>
                <span className="font-medium">Delivered: </span>
                {new Date(status.deliveredAt).toLocaleString()}
              </div>
            )}
            {status.bounceReason && (
              <div>
                <span className="font-medium">Bounce reason: </span>
                {status.bounceReason}
              </div>
            )}
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
