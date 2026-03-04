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
        isDebit && 'border-orange-200 bg-orange-100 text-orange-800',
        isCredit && 'border-blue-200 bg-blue-100 text-blue-800',
        !isDebit && !isCredit && 'border-gray-200 bg-gray-100 text-gray-800',
        className,
      )}
    >
      {direction}
    </span>
  )
}
