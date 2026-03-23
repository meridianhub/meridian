import { useParams } from 'react-router-dom'
import { Card } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Breadcrumbs, DetailSkeleton, ErrorState, PageShell } from '@/shared'
import { usePartyDetail } from '../hooks'
import { PartyHeader } from './components/party-header'
import { OverviewTab } from './tabs/overview-tab'
import { DemographicsTab } from './tabs/demographics-tab'
import { ReferencesTab } from './tabs/references-tab'
import { AssociationsTab } from './tabs/associations-tab'
import { BankRelationsTab } from './tabs/bank-relations-tab'
import { PaymentMethodsTab } from './tabs/payment-methods-tab'
import { AuditTrailTab } from './tabs/audit-trail-tab'
import { AccountsTab } from './tabs/accounts-tab'

export function PartyDetailPage() {
  const { partyId } = useParams<{ partyId: string }>()

  const { data: party, isLoading, isError, refetch } = usePartyDetail(partyId)

  if (!partyId) {
    return <div className="p-6 text-destructive">Party ID not found</div>
  }

  const partyLabel = party?.legalName ?? partyId

  return (
    <PageShell>
      <Breadcrumbs
        items={[
          { label: 'Parties', href: '/parties' },
          { label: partyLabel },
        ]}
      />

      {isLoading && <DetailSkeleton />}

      {!isLoading && (
        <>
          {isError && <ErrorState onRetry={refetch} />}

          <Card>
            <PartyHeader partyId={partyId} />
          </Card>

          <Card>
            <Tabs defaultValue="overview" className="w-full">
              <TabsList variant="line" className="w-full justify-start overflow-x-auto border-b">
                <TabsTrigger value="overview">Overview</TabsTrigger>
                <TabsTrigger value="demographics">Demographics</TabsTrigger>
                <TabsTrigger value="references">References</TabsTrigger>
                <TabsTrigger value="associations">Associations</TabsTrigger>
                <TabsTrigger value="bank-relations">Bank Relations</TabsTrigger>
                <TabsTrigger value="payment-methods">Payment Methods</TabsTrigger>
                <TabsTrigger value="accounts">Accounts</TabsTrigger>
                <TabsTrigger value="audit-trail">Audit Trail</TabsTrigger>
              </TabsList>

              <div className="p-6">
                <TabsContent value="overview" className="mt-0">
                  <OverviewTab partyId={partyId} />
                </TabsContent>

                <TabsContent value="demographics" className="mt-0">
                  <DemographicsTab partyId={partyId} />
                </TabsContent>

                <TabsContent value="references" className="mt-0">
                  <ReferencesTab partyId={partyId} />
                </TabsContent>

                <TabsContent value="associations" className="mt-0">
                  <AssociationsTab partyId={partyId} partyType={party?.partyType} />
                </TabsContent>

                <TabsContent value="bank-relations" className="mt-0">
                  <BankRelationsTab partyId={partyId} />
                </TabsContent>

                <TabsContent value="payment-methods" className="mt-0">
                  <PaymentMethodsTab partyId={partyId} />
                </TabsContent>

                <TabsContent value="accounts" className="mt-0">
                  <AccountsTab partyId={partyId} />
                </TabsContent>

                <TabsContent value="audit-trail" className="mt-0">
                  <AuditTrailTab partyId={partyId} />
                </TabsContent>
              </div>
            </Tabs>
          </Card>
        </>
      )}
    </PageShell>
  )
}
