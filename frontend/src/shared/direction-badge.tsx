import { ArrowDown, ArrowUp } from 'lucide-react'
import { cn } from '@/lib/utils'

interface DirectionBadgeProps {
  direction: string
}

export function DirectionBadge({ direction }: DirectionBadgeProps) {
  const isCredit = direction === 'CREDIT'

  return (
    <span
      data-testid="direction-badge"
      data-direction={direction}
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        isCredit
          ? 'bg-success-muted text-success-foreground border-success/30'
          : 'bg-destructive/10 text-destructive border-destructive/30',
      )}
    >
      {isCredit ? (
        <ArrowUp className="h-3 w-3" aria-hidden="true" />
      ) : (
        <ArrowDown className="h-3 w-3" aria-hidden="true" />
      )}
      {isCredit ? 'Credit' : 'Debit'}
    </span>
  )
}
