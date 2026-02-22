import { Badge } from '@/components/ui/badge'
import { MoneyDisplay } from '@/components/shared/money-display'

export const REASON_CODES = [
  'AMOUNT_MISMATCH',
  'MISSING_ENTRY',
  'DUPLICATE_ENTRY',
  'TIMING_DIFFERENCE',
  'CURRENCY_MISMATCH',
  'DIRECTION_ERROR',
  'QUALITY_UPGRADE',
  'EXTERNAL_MISMATCH',
  'CORRECTION_APPLIED',
] as const

export type ReasonCode = (typeof REASON_CODES)[number]

export interface VarianceSide {
  amount: string
  currency: string
  direction: string
  entryId?: string
}

export interface Variance {
  varianceId: string
  reasonCode: ReasonCode
  expected: VarianceSide | null
  actual: VarianceSide | null
  notes?: string
}

function SideDisplay({ side, label }: { side: VarianceSide | null; label: string }) {
  return (
    <div className="flex-1 rounded-md border p-3">
      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      {side ? (
        <div className="space-y-1">
          <div className="text-sm font-medium">
            <MoneyDisplay amount={side.amount} currency={side.currency} showSign={false} />
          </div>
          <div className="text-xs text-muted-foreground">
            Direction: {side.direction}
          </div>
          {side.entryId && (
            <div className="font-mono text-xs text-muted-foreground">
              Entry: {side.entryId}
            </div>
          )}
        </div>
      ) : (
        <div className="text-sm text-muted-foreground italic">No entry</div>
      )}
    </div>
  )
}

export function VarianceDetail({ variance }: { variance: Variance }) {
  return (
    <div
      data-testid="variance-detail"
      className="rounded-lg border bg-card p-4 shadow-sm"
    >
      <div className="mb-3 flex items-center gap-2">
        <span className="font-mono text-xs text-muted-foreground">
          {variance.varianceId}
        </span>
        <Badge variant="outline">{variance.reasonCode}</Badge>
      </div>
      <div className="flex gap-3">
        <SideDisplay side={variance.expected} label="Expected" />
        <SideDisplay side={variance.actual} label="Actual" />
      </div>
      {variance.notes && (
        <p className="mt-3 text-sm text-muted-foreground">{variance.notes}</p>
      )}
    </div>
  )
}
