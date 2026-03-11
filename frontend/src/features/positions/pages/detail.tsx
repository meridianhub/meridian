import * as React from 'react'
import { useParams } from 'react-router-dom'
import { Card } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { MoneyDisplay } from '@/shared/money-display'
import { TimeDisplay } from '@/shared/time-display'
import { QualityLadderBadge } from '@/features/positions/components/quality-ladder-badge'
import { DirectionBadge } from '@/shared/direction-badge'
import { EntityLink, Breadcrumbs, PageShell, DetailSkeleton, ErrorState } from '@/shared'
import { accountEntityType } from '@/shared/account-entity-type'
import { usePositionLogDetail } from '../hooks'
import type { FinancialPositionLog, TransactionLogEntry } from './index'

// Currencies with non-standard decimal places (ISO 4217)
const CURRENCY_PRECISION: Record<string, number> = {
  JPY: 0, KRW: 0, VND: 0, BHD: 3, KWD: 3, OMR: 3,
}

/**
 * Convert a google.type.Money value (or primitive bigint/string) to minor units
 * (e.g. cents for 2-decimal currencies) suitable for MoneyDisplay/formatMoney.
 * Returns null if the value cannot be converted.
 */
function toMinorUnits(money: unknown, currency: string): bigint | null {
  if (money === undefined || money === null) return null
  try {
    if (typeof money === 'bigint') return money
    if (typeof money === 'string') return BigInt(money)
    if (typeof money === 'object' && 'units' in money) {
      const m = money as { units?: bigint | number | string; nanos?: number }
      const units = typeof m.units === 'bigint' ? m.units : BigInt(m.units ?? 0)
      const nanos = m.nanos ?? 0
      const precision = CURRENCY_PRECISION[currency] ?? 2
      const factor = BigInt(10 ** precision)
      // Use BigInt arithmetic for rounding to avoid Math.round asymmetry on negative nanos
      const nanosDivisor = BigInt(10 ** (9 - precision))
      const nanosBig = BigInt(nanos)
      const half = nanosDivisor / 2n
      const roundedNanos =
        nanosBig >= 0n
          ? (nanosBig + half) / nanosDivisor
          : (nanosBig - half) / nanosDivisor
      return units * factor + roundedNanos
    }
  } catch {
    // Invalid BigInt conversion — skip
  }
  return null
}

function LabeledField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-sm font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm">{children}</dd>
    </div>
  )
}

interface BalanceViewProps {
  log: FinancialPositionLog
}

function BalanceView({ log }: BalanceViewProps) {
  const entries = log.transactionLogEntries ?? []
  const currency = entries[0]?.amount?.currency ?? 'USD'

  // Calculate provisional balance: sum of all entries (ESTIMATE, COEFFICIENT)
  // and available balance: sum of ACTUAL and REVISED entries only
  let provisionalTotal = 0n
  let availableTotal = 0n

  for (const entry of entries) {
    const entryCurrency = entry.amount?.currency ?? currency
    const amt = toMinorUnits(entry.amount?.amount, entryCurrency)
    if (amt === null) continue
    const signed = entry.direction === 'CREDIT' ? amt : -amt

    provisionalTotal += signed

    const quality = entry.qualityLevel ?? 'ESTIMATE'
    if (quality === 'ACTUAL' || quality === 'REVISED') {
      availableTotal += signed
    }
  }

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <Card className="p-4">
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          Provisional Balance
        </p>
        <p className="mt-2 text-2xl font-bold tabular-nums" data-testid="provisional-balance">
          <MoneyDisplay amount={provisionalTotal} currency={currency} showSign />
        </p>
        <p className="mt-1 text-xs text-muted-foreground">Includes estimates and coefficients</p>
      </Card>

      <Card className="p-4">
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          Available Balance
        </p>
        <p className="mt-2 text-2xl font-bold tabular-nums" data-testid="available-balance">
          <MoneyDisplay amount={availableTotal} currency={currency} showSign />
        </p>
        <p className="mt-1 text-xs text-muted-foreground">Actual and revised entries only</p>
      </Card>
    </div>
  )
}

interface MeasurementHistoryProps {
  entries: TransactionLogEntry[]
}

function MeasurementHistory({ entries }: MeasurementHistoryProps) {
  if (entries.length === 0) {
    return (
      <div
        data-testid="measurement-history-empty"
        className="flex h-32 items-center justify-center text-muted-foreground text-sm"
      >
        No measurement history available.
      </div>
    )
  }

  return (
    <div className="overflow-x-auto" data-testid="measurement-history-table">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-left">
            <th className="pb-2 pr-4 font-medium text-muted-foreground">Entry ID</th>
            <th className="pb-2 pr-4 font-medium text-muted-foreground">Direction</th>
            <th className="pb-2 pr-4 font-medium text-muted-foreground">Amount</th>
            <th className="pb-2 pr-4 font-medium text-muted-foreground">Quality</th>
            <th className="pb-2 pr-4 font-medium text-muted-foreground">Timestamp</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry) => (
            <tr
              key={entry.entryId}
              data-testid="measurement-entry"
              className="border-b last:border-0"
            >
              <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">
                {entry.entryId.slice(0, 8)}…
              </td>
              <td className="py-2 pr-4">
                <DirectionBadge direction={entry.direction} />
              </td>
              <td className="py-2 pr-4 tabular-nums">
                {(() => {
                  if (!entry.amount) return '—'
                  const minor = toMinorUnits(entry.amount.amount, entry.amount.currency)
                  if (minor === null) return '—'
                  return (
                    <MoneyDisplay
                      amount={minor}
                      currency={entry.amount.currency}
                      showSign={entry.direction === 'DEBIT'}
                    />
                  )
                })()}
              </td>
              <td className="py-2 pr-4">
                <QualityLadderBadge quality={entry.qualityLevel ?? 'ESTIMATE'} />
              </td>
              <td className="py-2 pr-4">
                <TimeDisplay timestamp={entry.timestamp} format="absolute" />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function PositionDetailPage() {
  const { logId } = useParams<{ logId: string }>()

  const { data, isLoading, isError, refetch } = usePositionLogDetail(logId)

  const log = data?.log as FinancialPositionLog | undefined

  if (isLoading) {
    return <DetailSkeleton fieldCount={5} tabCount={2} showBackNav />
  }

  if (isError || !log) {
    return (
      <PageShell>
        <Breadcrumbs
          items={[
            { label: 'Positions', href: '/positions' },
            { label: logId ?? 'Position Log' },
          ]}
        />
        <h1 className="text-3xl font-bold tracking-tight">Position Log</h1>
        <ErrorState
          message={isError ? 'Failed to load position log' : 'Position log not found'}
          onRetry={isError ? () => void refetch() : undefined}
        />
      </PageShell>
    )
  }

  return (
    <PageShell>
      <Breadcrumbs
        items={[
          { label: 'Positions', href: '/positions' },
          { label: log?.logId ?? logId ?? 'Position Log' },
        ]}
      />

      <div>
        <h1 className="text-3xl font-bold tracking-tight">Position Log</h1>
        {log && (
          <p className="mt-1 font-mono text-sm text-muted-foreground">{log.logId}</p>
        )}
      </div>

      {log && (
        <Card className="p-6">
          <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <LabeledField label="Log ID">
              <span className="font-mono text-xs">{log?.logId ?? '—'}</span>
            </LabeledField>

            <LabeledField label="Account ID">
              {log?.accountId ? (
                accountEntityType(log.accountServiceDomain) ? (
                  <EntityLink type={accountEntityType(log.accountServiceDomain)!} id={log.accountId} className="font-mono text-xs text-blue-600 hover:underline dark:text-blue-400" />
                ) : (
                  <span className="font-mono text-xs">{log.accountId}</span>
                )
              ) : (
                <span>—</span>
              )}
            </LabeledField>

            <LabeledField label="Status">
              <span>
                {typeof log?.statusTracking?.currentStatus === 'string' ? log.statusTracking.currentStatus.replace(/_/g, ' ') : '—'}
              </span>
            </LabeledField>

            <LabeledField label="Created">
              <TimeDisplay timestamp={log?.createdAt} />
            </LabeledField>

            <LabeledField label="Last Updated">
              <TimeDisplay timestamp={log?.updatedAt} />
            </LabeledField>
          </dl>
        </Card>
      )}

      {log && (
        <Tabs defaultValue="balance">
          <TabsList>
            <TabsTrigger value="balance">Balance View</TabsTrigger>
            <TabsTrigger value="history">Measurement History</TabsTrigger>
          </TabsList>

          <TabsContent value="balance" className="mt-4">
            <BalanceView log={log} />
          </TabsContent>

          <TabsContent value="history" className="mt-4">
            <Card className="p-6">
              <MeasurementHistory entries={log.transactionLogEntries ?? []} />
            </Card>
          </TabsContent>
        </Tabs>
      )}
    </PageShell>
  )
}
