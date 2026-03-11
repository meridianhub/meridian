import * as React from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { MoneyDisplay } from '@/shared/money-display'
import { AuditTrail, Breadcrumbs, PageShell, PageHeader, DetailSkeleton, ErrorState } from '@/shared'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { ControlAction } from '@/api/gen/meridian/internal_account/v1/internal_account_pb'
import { useInternalAccountDetail } from '../hooks'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type InternalAccountStatusLabel = 'ACTIVE' | 'SUSPENDED' | 'CLOSED' | 'UNKNOWN'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function accountStatusLabel(status: number): InternalAccountStatusLabel {
  switch (status) {
    case 1: return 'ACTIVE'
    case 2: return 'SUSPENDED'
    case 3: return 'CLOSED'
    default: return 'UNKNOWN'
  }
}

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
// Not found
// ---------------------------------------------------------------------------

function InternalAccountNotFound() {
  return (
    <PageShell>
      <Breadcrumbs items={[{ label: 'Internal Accounts', href: '/internal-accounts' }, { label: 'Not found' }]} />
      <ErrorState
        title="Account not found"
        message="The internal account you are looking for does not exist or has been removed."
      />
    </PageShell>
  )
}

// ---------------------------------------------------------------------------
// Action buttons
// ---------------------------------------------------------------------------

interface InternalAccountActionsProps {
  accountId: string
  accountStatus: number
  queryKey: readonly unknown[]
}

function InternalAccountActions({ accountId, accountStatus, queryKey }: InternalAccountActionsProps) {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const [actionError, setActionError] = React.useState<string | null>(null)

  const statusLabel = accountStatusLabel(accountStatus)

  const controlMutation = useMutation({
    mutationFn: (action: ControlAction) =>
      clients.internalAccount.controlInternalAccount({
        accountId,
        controlAction: action,
        reason: action === ControlAction.CONTROL_ACTION_SUSPEND ? 'Suspended by operator' : 'Reactivated by operator',
      }),
    onMutate: () => {
      setActionError(null)
    },
    onError: () => {
      setActionError('Failed to update account status. Please try again.')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey })
    },
  })

  if (statusLabel === 'CLOSED' || statusLabel === 'UNKNOWN') {
    return null
  }

  return (
    <div className="flex flex-col items-end gap-2">
      <div className="flex gap-2">
        {statusLabel === 'ACTIVE' && (
          <Button
            variant="outline"
            size="sm"
            disabled={controlMutation.isPending}
            onClick={() => controlMutation.mutate(ControlAction.CONTROL_ACTION_SUSPEND)}
          >
            Suspend
          </Button>
        )}
        {statusLabel === 'SUSPENDED' && (
          <Button
            variant="outline"
            size="sm"
            disabled={controlMutation.isPending}
            onClick={() => controlMutation.mutate(ControlAction.CONTROL_ACTION_ACTIVATE)}
          >
            Reactivate
          </Button>
        )}
      </div>
      {actionError && <p className="text-xs text-destructive">{actionError}</p>}
    </div>
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
// Transactions tab
// ---------------------------------------------------------------------------

function InternalAccountTransactions({ accountId, instrumentCode }: { accountId: string; instrumentCode: string }) {
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()

  const { data, isLoading, isError } = useQuery({
    queryKey: [...tenantKeys.internalAccount(tenantSlug ?? '', accountId), 'postings'],
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
          <>
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
            {postings.length >= 50 && (
              <p className="mt-2 text-xs text-muted-foreground">
                Showing the most recent 50 transactions. Older transactions may not be displayed.
              </p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export function InternalAccountDetailPage() {
  const { accountId } = useParams<{ accountId: string }>()
  const { tenantSlug } = useTenantContext()

  const queryKey = tenantKeys.internalAccount(tenantSlug ?? '', accountId ?? '')

  const { data: account, isLoading, isError, refetch } = useInternalAccountDetail(accountId)

  if (isLoading) {
    return <DetailSkeleton />
  }

  if (isError) {
    return (
      <PageShell>
        <Breadcrumbs items={[{ label: 'Internal Accounts', href: '/internal-accounts' }, { label: 'Error' }]} />
        <ErrorState onRetry={refetch} />
      </PageShell>
    )
  }

  if (account === null || account === undefined) {
    return <InternalAccountNotFound />
  }

  const statusLabel = accountStatusLabel(account.accountStatus)

  return (
    <PageShell>
      <Breadcrumbs
        items={[
          { label: 'Internal Accounts', href: '/internal-accounts' },
          { label: account.accountCode },
        ]}
      />

      <PageHeader
        title={account.accountCode}
        description={account.name}
        actions={
          <InternalAccountActions
            accountId={account.accountId}
            accountStatus={account.accountStatus}
            queryKey={queryKey}
          />
        }
      />

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="transactions">Transactions</TabsTrigger>
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
                <DetailField label="Account Code">
                  <span className="font-mono">{account.accountCode}</span>
                </DetailField>
                <DetailField label="Name">{account.name}</DetailField>
                <DetailField label="Type (Behavior Class)">{account.behaviorClass}</DetailField>
                <DetailField label="Instrument">
                  <span className="font-mono">{account.instrumentCode}</span>
                </DetailField>
                <DetailField label="Status">
                  <StatusBadge status={statusLabel} />
                </DetailField>
                {account.description && (
                  <DetailField label="Description">{account.description}</DetailField>
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
          <InternalAccountTransactions
            accountId={account.accountId}
            instrumentCode={account.instrumentCode}
          />
        </TabsContent>

        <TabsContent value="audit" className="mt-4">
          <Card>
            <CardHeader>
              <CardTitle>Audit Trail</CardTitle>
            </CardHeader>
            <CardContent>
              <AuditTrail entityType="InternalAccount" entityId={account.accountId} />
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </PageShell>
  )
}
