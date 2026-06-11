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
        // Credit = filled (green tint), debit = outlined (card + ink border),
        // always with the DEBIT/CREDIT text label — never hue alone.
        isDebit && 'border-foreground/40 bg-card text-foreground',
        isCredit && 'border-success/40 bg-success-muted text-success-foreground',
        !isDebit && !isCredit && 'border-border bg-muted text-muted-foreground',
        className,
      )}
    >
      {direction}
    </span>
  )
}
