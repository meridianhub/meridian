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
      className={cn('rounded-lg border p-4', isBalanced ? 'border-success/30 bg-success-muted' : 'border-destructive/30 bg-destructive/10', className)}
    >
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2">
          <span
            className={cn(
              'inline-flex h-5 w-5 items-center justify-center rounded-full text-xs font-bold',
              isBalanced ? 'bg-success text-success-foreground' : 'bg-destructive text-destructive-foreground',
            )}
          >
            {isBalanced ? '✓' : '✗'}
          </span>
          <span
            className={cn(
              'text-sm font-semibold',
              isBalanced ? 'text-success-foreground' : 'text-destructive',
            )}
          >
            {isBalanced ? 'Balanced' : 'Unbalanced'}
          </span>
        </div>

        {!isBalanced && (
          <span
            data-testid="balance-difference"
            className="text-sm text-destructive"
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
