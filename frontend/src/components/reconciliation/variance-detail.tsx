import { Badge } from '@/components/ui/badge'

export type VarianceReason =
  | 'VARIANCE_REASON_UNSPECIFIED'
  | 'VARIANCE_REASON_AMOUNT_MISMATCH'
  | 'VARIANCE_REASON_MISSING_ENTRY'
  | 'VARIANCE_REASON_DUPLICATE_ENTRY'
  | 'VARIANCE_REASON_TIMING_DIFFERENCE'
  | 'VARIANCE_REASON_CURRENCY_MISMATCH'
  | 'VARIANCE_REASON_DIRECTION_ERROR'
  | 'VARIANCE_REASON_OTHER'

export type VarianceStatus =
  | 'VARIANCE_STATUS_UNSPECIFIED'
  | 'VARIANCE_STATUS_OPEN'
  | 'VARIANCE_STATUS_INVESTIGATING'
  | 'VARIANCE_STATUS_DISPUTED'
  | 'VARIANCE_STATUS_RESOLVED'
  | 'VARIANCE_STATUS_ACCEPTED'

export interface Variance {
  varianceId: string
  runId: string
  snapshotId: string
  accountId: string
  instrumentCode: string
  expectedAmount: string
  actualAmount: string
  varianceAmount: string
  reason: VarianceReason
  status: VarianceStatus
  resolutionNote?: string
  resolvedBy?: string
  resolvedAt?: string
  createdAt: string
  updatedAt: string
}

export function VarianceDetail({ variance }: { variance: Variance }) {
  const reasonLabel = variance.reason.replace('VARIANCE_REASON_', '')
  const statusLabel = variance.status.replace('VARIANCE_STATUS_', '')

  return (
    <div
      data-testid="variance-detail"
      className="rounded-lg border bg-card p-4 shadow-sm"
    >
      <div className="mb-3 flex items-center gap-2">
        <span className="font-mono text-xs text-muted-foreground">
          {variance.varianceId}
        </span>
        <Badge variant="outline">{reasonLabel}</Badge>
        <Badge
          variant={
            variance.status === 'VARIANCE_STATUS_OPEN'
              ? 'destructive'
              : variance.status === 'VARIANCE_STATUS_RESOLVED' ||
                  variance.status === 'VARIANCE_STATUS_ACCEPTED'
                ? 'secondary'
                : 'outline'
          }
        >
          {statusLabel}
        </Badge>
      </div>

      <div className="flex gap-3">
        <div className="flex-1 rounded-md border p-3">
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Expected
          </div>
          <div className="text-sm font-medium">{variance.expectedAmount}</div>
        </div>
        <div className="flex-1 rounded-md border p-3">
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Actual
          </div>
          <div className="text-sm font-medium">{variance.actualAmount}</div>
        </div>
        <div className="flex-1 rounded-md border p-3">
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Variance
          </div>
          <div className="text-sm font-medium">{variance.varianceAmount}</div>
        </div>
      </div>

      <div className="mt-2 text-xs text-muted-foreground">
        Account: {variance.accountId} | Instrument: {variance.instrumentCode}
      </div>

      {variance.resolutionNote && (
        <div className="mt-3 rounded-md bg-muted p-2 text-sm text-muted-foreground">
          {variance.resolutionNote}
        </div>
      )}
    </div>
  )
}
