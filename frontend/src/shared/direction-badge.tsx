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
        // Credit = filled (green tint), debit = outlined (card + ink border).
        // Never hue alone: the fill/outline pairing plus the text label carry
        // the meaning for colour-blind users. Debit is not an error state, so
        // no destructive red here.
        isCredit
          ? 'bg-success-muted text-success-foreground border-success/40'
          : 'bg-card text-foreground border-foreground/40',
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
