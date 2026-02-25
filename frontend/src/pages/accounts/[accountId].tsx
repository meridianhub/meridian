import * as React from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { ChevronLeftIcon } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { AuditTrail } from '@/components/shared'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { DepositDialog } from './deposit-dialog'
import { WithdrawDialog } from './withdraw-dialog'
import { ControlDialog } from './control-dialog'
import type { ControlAction } from './control-dialog'
import { CreateLienDialog } from './create-lien-dialog'
import type { AccountStatus, CurrentAccount, RetrieveCurrentAccountResponse } from './types'

async function retrieveAccount(
  tenantSlug: string,
  accountId: string,
): Promise<CurrentAccount | null> {
  const response = await fetch(
    `/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-Slug': tenantSlug,
      },
      body: JSON.stringify({ accountId }),
    },
  )

  if (response.status === 404) {
    return null
  }

  if (!response.ok) {
    throw new Error(`Failed to retrieve account: ${response.status}`)
  }

  const data = (await response.json()) as RetrieveCurrentAccountResponse
  return data.account
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
  status: AccountStatus
  accountId: string
  currency: string
}

function AccountActions({ status, accountId, currency }: AccountActionsProps) {
  const [depositOpen, setDepositOpen] = React.useState(false)
  const [withdrawOpen, setWithdrawOpen] = React.useState(false)
  const [lienOpen, setLienOpen] = React.useState(false)
  const [controlOpen, setControlOpen] = React.useState(false)
  const [controlAction, setControlAction] = React.useState<ControlAction>('freeze')

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
// Main page
// ---------------------------------------------------------------------------

export function AccountDetailPage() {
  const { accountId } = useParams<{ accountId: string }>()
  const { tenantSlug } = useTenantContext()

  const { data: account, isLoading, isError } = useQuery({
    queryKey: tenantKeys.account(tenantSlug ?? '', accountId ?? ''),
    queryFn: () => retrieveAccount(tenantSlug ?? '', accountId ?? ''),
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
          {account.iban && (
            <p className="mt-1 font-mono text-sm text-muted-foreground">{account.iban}</p>
          )}
        </div>
        <AccountActions
          status={account.status}
          accountId={account.accountId}
          currency={account.baseCurrency}
        />
      </div>

      {/* Summary fields */}
      <Card className="mt-6">
        <CardContent>
          <dl className="grid grid-cols-2 gap-4 pt-2 md:grid-cols-4">
            <DetailField label="Currency">{account.baseCurrency}</DetailField>
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
                  <DetailField label="IBAN">{account.iban || '—'}</DetailField>
                  <DetailField label="Status">
                    <StatusBadge status={account.status} />
                  </DetailField>
                  <DetailField label="Base Currency">{account.baseCurrency}</DetailField>
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
            <Card>
              <CardHeader>
                <CardTitle>Transactions</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-sm text-muted-foreground">
                  Transaction history for this account will appear here.
                </p>
              </CardContent>
            </Card>
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
