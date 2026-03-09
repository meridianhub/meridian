import * as React from 'react'
import { useParams, Navigate } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { MoneyDisplay } from '@/shared/money-display'
import { AuditTrail, EntityLink, Breadcrumbs } from '@/shared'
import { useAccountResolver } from '@/shared/use-account-resolver'
import { DepositDialog } from './deposit-dialog'
import { WithdrawDialog } from './withdraw-dialog'
import { ControlDialog } from './control-dialog'
import type { ControlAction } from './control-dialog'
import { CreateLienDialog } from './create-lien-dialog'
import { CreateValuationFeatureDialog } from '@/features/reference-data/components/create-valuation-feature-dialog'
import type { AccountStatus as AccountStatusType } from './types'
import { useAccountDetail, useAccountPostings, useAccountLiens } from '../hooks'

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

function AccountNotFound({ accountId }: { accountId?: string }) {
  return (
    <div data-testid="account-not-found" className="p-6">
      <Breadcrumbs items={[{ label: 'Accounts', href: '/accounts' }, { label: 'Not found' }]} />
      <div className="mt-8 text-center">
        <h2 className="text-xl font-semibold">Account not found</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          {accountId
            ? `Account "${accountId}" was not found in any account service.`
            : 'The account you are looking for does not exist or has been removed.'}
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
// Enum helpers
// ---------------------------------------------------------------------------

function getDirectionName(direction: unknown): string {
  if (typeof direction === 'string') return direction
  if (typeof direction === 'number') {
    const dirMap: Record<number, string> = { 0: 'UNSPECIFIED', 1: 'DEBIT', 2: 'CREDIT' }
    return dirMap[direction] ?? String(direction)
  }
  return String(direction ?? '')
}

function getTransactionStatusName(status: unknown): string {
  if (typeof status === 'string') return status
  if (typeof status === 'number') {
    const statusMap: Record<number, string> = { 0: 'UNSPECIFIED', 1: 'PENDING', 2: 'POSTED', 3: 'FAILED', 4: 'CANCELLED', 5: 'REVERSED' }
    return statusMap[status] ?? String(status)
  }
  return String(status ?? '')
}

// ---------------------------------------------------------------------------
// Transactions (ledger postings for this account)
// ---------------------------------------------------------------------------

function AccountTransactions({ accountId, instrumentCode }: { accountId: string; instrumentCode: string }) {
  const { data, isLoading, isError } = useAccountPostings(accountId)

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
                      <StatusBadge status={getDirectionName(p.postingDirection)} />
                    </td>
                    <td className="py-2 pr-4 tabular-nums">
                      <MoneyDisplay
                        amount={p.postingAmount?.units}
                        currency={p.postingAmount?.currencyCode ?? instrumentCode}
                      />
                    </td>
                    <td className="py-2 pr-4">
                      <StatusBadge status={getTransactionStatusName(p.status)} />
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
// Liens
// ---------------------------------------------------------------------------

function getLienTypeName(blockType: number): string {
  const typeMap: Record<number, string> = {
    0: 'UNSPECIFIED',
    1: 'PENDING',
    2: 'FINAL',
    3: 'TEMPORARY',
  }
  return typeMap[blockType] ?? String(blockType)
}

function AccountLiens({ accountId, instrumentCode }: { accountId: string; instrumentCode: string }) {
  const { data, isLoading, isError } = useAccountLiens(accountId)

  const blocks = data?.blocks ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle>Liens</CardTitle>
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
          <p className="text-sm text-muted-foreground">Failed to load liens.</p>
        )}
        {!isLoading && !isError && blocks.length === 0 && (
          <p className="text-sm text-muted-foreground">No active liens for this account.</p>
        )}
        {!isLoading && !isError && blocks.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-xs font-medium text-muted-foreground">
                  <th className="pb-2 pr-4">Lien ID</th>
                  <th className="pb-2 pr-4">Amount</th>
                  <th className="pb-2 pr-4">Type</th>
                  <th className="pb-2 pr-4">Purpose</th>
                  <th className="pb-2">Expiry</th>
                </tr>
              </thead>
              <tbody>
                {blocks.map((block) => (
                  <tr key={block.blockId} className="border-b last:border-0">
                    <td className="py-2 pr-4 font-mono text-xs">{block.blockId}</td>
                    <td className="py-2 pr-4 tabular-nums">
                      <MoneyDisplay
                        amount={block.amount?.amount?.units}
                        currency={block.amount?.amount?.currencyCode ?? instrumentCode}
                      />
                    </td>
                    <td className="py-2 pr-4">
                      <StatusBadge status={getLienTypeName(block.blockType)} />
                    </td>
                    <td className="py-2 pr-4 max-w-xs truncate" title={block.purpose}>
                      {block.purpose || '—'}
                    </td>
                    <td className="py-2">
                      {block.expiresAt
                        ? <TimeDisplay timestamp={block.expiresAt} format="absolute" />
                        : <span className="text-muted-foreground">—</span>}
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

  const { data: account, isLoading, isError, refetch, isFetching } = useAccountDetail(accountId)
  const { data: resolved, isLoading: isResolving } = useAccountResolver(
    // Only resolve when current-account returns 404 (null)
    account === null && !isLoading ? accountId : undefined,
    // Skip current-account check — useAccountDetail already tried it
    { skipServices: ['current'] },
  )

  if (isLoading) {
    return <AccountDetailSkeleton />
  }

  // Current-account returned 404 — check if it's an internal account
  if (account === null) {
    if (isResolving) {
      return <AccountDetailSkeleton />
    }
    if (resolved?.type === 'internal') {
      return <Navigate to={`/internal-accounts/${encodeURIComponent(resolved.accountId)}`} replace />
    }
    return <AccountNotFound accountId={accountId} />
  }

  if (isError || account === undefined) {
    return (
      <div data-testid="account-error" className="p-6">
        <Breadcrumbs items={[{ label: 'Accounts', href: '/accounts' }, { label: accountId ?? 'Error' }]} />
        <div className="mt-8 text-center">
          <h2 className="text-xl font-semibold">Failed to load account</h2>
          <p className="mt-2 text-sm text-muted-foreground">
            There was a problem loading this account. Please try again.
          </p>
          <Button
            variant="outline"
            size="sm"
            className="mt-4"
            disabled={isFetching}
            onClick={() => void refetch()}
          >
            {isFetching ? 'Retrying…' : 'Retry'}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6">
      {/* Breadcrumb navigation */}
      <Breadcrumbs
        items={[
          { label: 'Accounts', href: '/accounts' },
          { label: account.accountId },
        ]}
      />

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
              <DetailField label="Party ID">
                <EntityLink type="party" id={account.partyId} />
              </DetailField>
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
                    <DetailField label="Party ID">
                      <EntityLink type="party" id={account.partyId} />
                    </DetailField>
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
            <AccountLiens accountId={account.accountId} instrumentCode={account.instrumentCode} />
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
