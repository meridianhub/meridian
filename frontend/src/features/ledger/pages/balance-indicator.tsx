import { MoneyDisplay } from '@/shared/money-display'
import { cn } from '@/lib/utils'

interface BalanceIndicatorProps {
  debitTotal: bigint
  creditTotal: bigint
  currency: string
  className?: string
}

export function BalanceIndicator({
  debitTotal,
  creditTotal,
  currency,
  className,
}: BalanceIndicatorProps) {
  const isBalanced = debitTotal === creditTotal
  const difference = debitTotal > creditTotal ? debitTotal - creditTotal : creditTotal - debitTotal

  return (
    <div
      data-testid="balance-indicator"
      data-balanced={isBalanced.toString()}
      className={cn('rounded-lg border p-4', isBalanced ? 'border-green-200 bg-green-50' : 'border-red-200 bg-red-50', className)}
    >
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2">
          <span
            className={cn(
              'inline-flex h-5 w-5 items-center justify-center rounded-full text-xs font-bold text-white',
              isBalanced ? 'bg-green-600' : 'bg-red-600',
            )}
          >
            {isBalanced ? '✓' : '✗'}
          </span>
          <span
            className={cn(
              'text-sm font-semibold',
              isBalanced ? 'text-green-800' : 'text-red-800',
            )}
          >
            {isBalanced ? 'Balanced' : 'Unbalanced'}
          </span>
        </div>

        {!isBalanced && (
          <span
            data-testid="balance-difference"
            className="text-sm text-red-700"
          >
            Difference: <MoneyDisplay amount={difference} currency={currency} />
          </span>
        )}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-4">
        <div>
          <p className="text-xs text-muted-foreground">Total Debits</p>
          <p data-testid="debit-total" className="text-sm font-medium tabular-nums">
            <MoneyDisplay amount={debitTotal} currency={currency} />
          </p>
        </div>
        <div>
          <p className="text-xs text-muted-foreground">Total Credits</p>
          <p data-testid="credit-total" className="text-sm font-medium tabular-nums">
            <MoneyDisplay amount={creditTotal} currency={currency} />
          </p>
        </div>
      </div>
    </div>
  )
}
