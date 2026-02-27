import * as React from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { ChevronLeftIcon } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { MoneyDisplay } from '@/components/shared/money-display'
import { AuditTrail } from '@/components/shared'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { AccountStatus } from '@/api/gen/meridian/current_account/v1/current_account_pb'
import { DepositDialog } from './deposit-dialog'
import { WithdrawDialog } from './withdraw-dialog'
import { ControlDialog } from './control-dialog'
import type { ControlAction } from './control-dialog'
import { CreateLienDialog } from './create-lien-dialog'
import { CreateValuationFeatureDialog } from '@/components/shared/create-valuation-feature-dialog'
import type { AccountStatus as AccountStatusType, CurrentAccount } from './types'

const ACCOUNT_STATUS_NAMES: Record<number, string> = {
  [AccountStatus.ACTIVE]: 'ACTIVE',
  [AccountStatus.FROZEN]: 'FROZEN',
  [AccountStatus.CLOSED]: 'CLOSED',
}

/** Extract a display string from google.type.Money (units + nanos/1e9). */
function formatBalance(money: { units?: bigint | number; nanos?: number; currencyCode?: string } | undefined | null): string | undefined {
  if (!money) return undefined
  const rawUnits = typeof money.units === 'bigint' ? money.units : BigInt(Math.trunc(money.units ?? 0))
  if (rawUnits > BigInt(Number.MAX_SAFE_INTEGER) || rawUnits < BigInt(Number.MIN_SAFE_INTEGER)) {
    return money.currencyCode ? `${money.currencyCode} ${rawUnits.toString()}` : rawUnits.toString()
  }
  const units = Number(rawUnits)
  const nanos = money.nanos ?? 0
  const value = units + nanos / 1e9
  if (money.currencyCode) {
    try {
      return new Intl.NumberFormat(undefined, { style: 'currency', currency: money.currencyCode }).format(value)
    } catch { /* fall through for non-ISO codes */ }
  }
  return value.toFixed(2)
}

// ---------------------------------------------------------------------------
// Skeleton
// ---------------------------------------------------------------------------

function AccountDetailSkeleton() {
  return (
    <div data-testid="account-detail-skeleton" className="animate-pulse space-y-6 p-6">
      <div className="flex items-center gap-3">
        <div className="h-4 w-24 rounded bg-muted" />
      </div>
      <div className="h-8 w-64 rounded bg-muted" />
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-20 rounded bg-muted" />
        ))}
      </div>
      <div className="h-64 rounded bg-muted" />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Not found
// ---------------------------------------------------------------------------

function AccountNotFound() {
  return (
    <div data-testid="account-not-found" className="p-6">
      <Link
        to="/accounts"
        className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        aria-label="Back to Accounts"
      >
        <ChevronLeftIcon className="h-4 w-4" />
        Accounts
      </Link>
      <div className="mt-8 text-center">
        <h2 className="text-xl font-semibold">Account not found</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          The account you are looking for does not exist or has been removed.
        </p>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Action buttons based on account status
// ---------------------------------------------------------------------------

interface AccountActionsProps {
  status: AccountStatusType
  accountId: string
  currency: string
}

function AccountActions({ status, accountId, currency }: AccountActionsProps) {
  const [depositOpen, setDepositOpen] = React.useState(false)
  const [withdrawOpen, setWithdrawOpen] = React.useState(false)
  const [lienOpen, setLienOpen] = React.useState(false)
  const [controlOpen, setControlOpen] = React.useState(false)
  const [controlAction, setControlAction] = React.useState<ControlAction>('freeze')
  const [valuationFeatureOpen, setValuationFeatureOpen] = React.useState(false)

  if (status === 'CLOSED' || status === 'SUSPENDED') {
    return null
  }

  function openControl(action: ControlAction) {
    setControlAction(action)
    setControlOpen(true)
  }

  return (
    <>
      <div className="flex gap-2">
        {status === 'ACTIVE' && (
          <>
            <Button variant="outline" size="sm" onClick={() => setDepositOpen(true)}>
              Deposit
            </Button>
            <Button variant="outline" size="sm" onClick={() => setWithdrawOpen(true)}>
              Withdraw
            </Button>
            <Button variant="outline" size="sm" onClick={() => setLienOpen(true)}>
              Create Lien
            </Button>
            <Button variant="outline" size="sm" onClick={() => setValuationFeatureOpen(true)}>
              Add Valuation Feature
            </Button>
            <Button variant="outline" size="sm" onClick={() => openControl('freeze')}>
              Freeze
            </Button>
            <Button variant="destructive" size="sm" onClick={() => openControl('close')}>
              Close Account
            </Button>
          </>
        )}
        {status === 'FROZEN' && (
          <Button variant="outline" size="sm" onClick={() => openControl('unfreeze')}>
            Unfreeze
          </Button>
        )}
      </div>

      <DepositDialog
        open={depositOpen}
        onOpenChange={setDepositOpen}
        accountId={accountId}
        currency={currency}
      />

      <WithdrawDialog
        open={withdrawOpen}
        onOpenChange={setWithdrawOpen}
        accountId={accountId}
        currency={currency}
      />

      <CreateLienDialog
        open={lienOpen}
        onOpenChange={setLienOpen}
        accountId={accountId}
        instrumentCode={currency}
        accountType="current"
      />

      <ControlDialog
        open={controlOpen}
        onOpenChange={setControlOpen}
        accountId={accountId}
        action={controlAction}
      />

      <CreateValuationFeatureDialog
        open={valuationFeatureOpen}
        onOpenChange={setValuationFeatureOpen}
        accountId={accountId}
        accountType="current"
        accountCurrency={currency}
      />
    </>
  )
}

// ---------------------------------------------------------------------------
// Detail field
// ---------------------------------------------------------------------------

function DetailField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm font-medium">{children}</dd>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Transactions (ledger postings for this account)
// ---------------------------------------------------------------------------

function AccountTransactions({ accountId, instrumentCode }: { accountId: string; instrumentCode: string }) {
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()

  const { data, isLoading, isError } = useQuery({
    queryKey: [...tenantKeys.account(tenantSlug ?? '', accountId), 'postings'],
    queryFn: () =>
      clients.financialAccounting.listLedgerPostings({
        pagination: { pageSize: 50, pageToken: '' },
        accountId,
      }),
    enabled: !!accountId,
  })

  const postings = data?.ledgerPostings ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle>Transactions</CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading && (
          <div className="animate-pulse space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="h-8 rounded bg-muted" />
            ))}
          </div>
        )}
        {isError && (
          <p className="text-sm text-muted-foreground">Failed to load transactions.</p>
        )}
        {!isLoading && !isError && postings.length === 0 && (
          <p className="text-sm text-muted-foreground">No transactions found for this account.</p>
        )}
        {!isLoading && !isError && postings.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-xs font-medium text-muted-foreground">
                  <th className="pb-2 pr-4">Direction</th>
                  <th className="pb-2 pr-4">Amount</th>
                  <th className="pb-2 pr-4">Status</th>
                  <th className="pb-2">Created</th>
                </tr>
              </thead>
              <tbody>
                {postings.map((p) => (
                  <tr key={p.id} className="border-b last:border-0">
                    <td className="py-2 pr-4">
                      <StatusBadge status={String(p.postingDirection ?? '')} />
                    </td>
                    <td className="py-2 pr-4 tabular-nums">
                      <MoneyDisplay
                        amount={p.postingAmount?.amount?.units}
                        currency={p.postingAmount?.amount?.currencyCode ?? instrumentCode}
                      />
                    </td>
                    <td className="py-2 pr-4">
                      <StatusBadge status={String(p.status ?? '')} />
                    </td>
                    <td className="py-2">
                      <TimeDisplay timestamp={p.createdAt} format="relative" />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export function AccountDetailPage() {
  const { accountId } = useParams<{ accountId: string }>()
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()

  const { data: account, isLoading, isError } = useQuery({
    queryKey: tenantKeys.account(tenantSlug ?? '', accountId ?? ''),
    queryFn: async (): Promise<CurrentAccount | null> => {
      try {
        const response = await clients.currentAccount.retrieveCurrentAccount({ accountId: accountId ?? '' })
        const f = response.facility
        if (!f) return null
        return {
          accountId: f.accountId,
          externalReference: f.externalIdentifier ?? '',
          status: (ACCOUNT_STATUS_NAMES[f.accountStatus] ?? String(f.accountStatus)) as AccountStatusType,
          instrumentCode: f.instrumentCode || '',
          availableBalance: formatBalance(f.currentBalance?.availableBalance?.amount),
          createdAt: f.createdAt ?? undefined,
          updatedAt: f.updatedAt ?? undefined,
          partyId: f.orgPartyId || undefined,
        }
      } catch (err: unknown) {
        if (ConnectError.from(err).code === Code.NotFound) return null
        throw err
      }
    },
    enabled: !!accountId,
  })

  if (isLoading) {
    return <AccountDetailSkeleton />
  }

  // null = 404 from server; isError = network/server failure; undefined = query not yet resolved
  if (isError || account === null || account === undefined) {
    return <AccountNotFound />
  }

  return (
    <div className="p-6">
      {/* Back navigation */}
      <Link
        to="/accounts"
        className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        aria-label="Back to Accounts"
      >
        <ChevronLeftIcon className="h-4 w-4" />
        Accounts
      </Link>

      {/* Page header */}
      <div className="mt-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-semibold">{account.accountId}</h1>
            <StatusBadge status={account.status} />
          </div>
          {account.externalReference && (
            <p className="mt-1 font-mono text-sm text-muted-foreground">{account.externalReference}</p>
          )}
        </div>
        <AccountActions
          status={account.status}
          accountId={account.accountId}
          currency={account.instrumentCode}
        />
      </div>

      {/* Summary fields */}
      <Card className="mt-6">
        <CardContent>
          <dl className="grid grid-cols-2 gap-4 pt-2 md:grid-cols-4">
            <DetailField label="Instrument">{account.instrumentCode}</DetailField>
            <DetailField label="Available Balance">
              {account.availableBalance ?? '—'}
            </DetailField>
            {account.partyId && (
              <DetailField label="Party ID">{account.partyId}</DetailField>
            )}
            <DetailField label="Created">
              <TimeDisplay timestamp={account.createdAt} format="absolute" />
            </DetailField>
          </dl>
        </CardContent>
      </Card>

      {/* Tabs */}
      <div className="mt-6">
        <Tabs defaultValue="overview">
          <TabsList>
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="transactions">Transactions</TabsTrigger>
            <TabsTrigger value="liens">Liens</TabsTrigger>
            <TabsTrigger value="audit">Audit Trail</TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="mt-4">
            <Card>
              <CardHeader>
                <CardTitle>Account Details</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <DetailField label="Account ID">{account.accountId}</DetailField>
                  <DetailField label="External Reference">{account.externalReference || '—'}</DetailField>
                  <DetailField label="Status">
                    <StatusBadge status={account.status} />
                  </DetailField>
                  <DetailField label="Instrument">{account.instrumentCode}</DetailField>
                  <DetailField label="Available Balance">
                    {account.availableBalance ?? '—'}
                  </DetailField>
                  <DetailField label="Reserved Balance">
                    {account.reservedBalance ?? '—'}
                  </DetailField>
                  {account.name && (
                    <DetailField label="Name">{account.name}</DetailField>
                  )}
                  {account.partyId && (
                    <DetailField label="Party ID">{account.partyId}</DetailField>
                  )}
                  <DetailField label="Created">
                    <TimeDisplay timestamp={account.createdAt} format="both" />
                  </DetailField>
                  <DetailField label="Last Updated">
                    <TimeDisplay timestamp={account.updatedAt} format="both" />
                  </DetailField>
                </dl>
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="transactions" className="mt-4">
            <AccountTransactions accountId={account.accountId} instrumentCode={account.instrumentCode} />
          </TabsContent>

          <TabsContent value="liens" className="mt-4">
            <Card>
              <CardHeader>
                <CardTitle>Liens</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-sm text-muted-foreground">
                  Active liens and reservations for this account will appear here.
                </p>
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="audit" className="mt-4">
            <Card>
              <CardHeader>
                <CardTitle>Audit Trail</CardTitle>
              </CardHeader>
              <CardContent>
                <AuditTrail entityType="CurrentAccount" entityId={account.accountId} />
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
