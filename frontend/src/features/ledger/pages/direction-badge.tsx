import { cn } from '@/lib/utils'

interface DirectionBadgeProps {
  direction: string
  className?: string
}

export function DirectionBadge({ direction, className }: DirectionBadgeProps) {
  const isDebit = direction === 'DEBIT'
  const isCredit = direction === 'CREDIT'

  return (
    <span
      data-testid="direction-badge"
      data-direction={direction}
      className={cn(
        'inline-flex items-center justify-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        isDebit && 'border-warning/30 bg-warning-muted text-warning-foreground',
        isCredit && 'border-info/30 bg-info-muted text-info-foreground',
        !isDebit && !isCredit && 'border-border bg-muted text-muted-foreground',
        className,
      )}
    >
      {direction}
    </span>
  )
}
