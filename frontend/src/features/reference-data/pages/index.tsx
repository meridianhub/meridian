import * as React from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { ChevronRight, Layers, Tag, GitBranch } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useApiClients } from '@/api/context'
import { referenceKeys } from '@/lib/query-keys'
import { InstrumentStatus } from '@/api/gen/meridian/reference_data/v1/instrument_pb'
import { BehaviorClass } from '@/api/gen/meridian/reference_data/v1/account_type_pb'
import { usePageTitle } from '@/hooks/use-page-title'

interface ReferenceDataCardProps {
  title: string
  description: string
  count: number | undefined
  isLoading: boolean
  isError: boolean
  href: string
  icon: React.ReactNode
}

function ReferenceDataCard({ title, description, count, isLoading, isError, href, icon }: ReferenceDataCardProps) {
  return (
    <Link to={href} className="group block focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-lg">
      <Card className="h-full transition-colors group-hover:border-primary/50 group-focus-visible:border-primary/50">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
          <div className="text-muted-foreground">{icon}</div>
        </CardHeader>
        <CardContent>
          <div className="mb-2">
            {isLoading ? (
              <div className="h-8 w-16 animate-pulse rounded bg-muted" />
            ) : isError ? (
              <div className="text-sm text-destructive">Failed to load</div>
            ) : (
              <div className="text-2xl font-bold">
                {count !== undefined ? count.toLocaleString() : '—'}
              </div>
            )}
          </div>
          <p className="text-xs text-muted-foreground">{description}</p>
          <div className="mt-4 flex items-center text-xs font-medium text-primary">
            View all
            <ChevronRight className="ml-1 h-3.5 w-3.5 transition-transform group-hover:translate-x-0.5" />
          </div>
        </CardContent>
      </Card>
    </Link>
  )
}

export function ReferenceDataHubPage() {
  usePageTitle('Reference Data')
  const clients = useApiClients()

  // Reference data APIs return no totalCount field, so we fetch all items to count them.
  // These collections are typically small (< 200 items), so fetching all is acceptable.
  const instrumentsQuery = useQuery({
    queryKey: [...referenceKeys.instruments(), 'hub-count'],
    queryFn: async () => {
      const res = await clients.referenceData.listInstruments({
        statusFilter: InstrumentStatus.UNSPECIFIED,
        pageSize: 1000,
        pageToken: '',
      })
      return res.instruments
    },
    staleTime: 60_000,
  })

  const accountTypesQuery = useQuery({
    queryKey: [...referenceKeys.accountTypes(), 'hub-count'],
    queryFn: async () => {
      const res = await clients.accountTypeRegistry.listActive({
        behaviorClassFilter: BehaviorClass.UNSPECIFIED,
        pageSize: 1000,
        pageToken: '',
      })
      return res.definitions
    },
    staleTime: 60_000,
  })

  const nodesQuery = useQuery({
    queryKey: [...referenceKeys.nodeChildren(''), 'hub-count'],
    queryFn: async () => {
      const res = await clients.node.getChildren({
        parentId: '',
        activeOnly: true,
      })
      return res.nodes
    },
    staleTime: 60_000,
  })

  const cards = [
    {
      title: 'Instruments',
      description: 'Asset classes and financial instrument definitions with CEL validation.',
      count: instrumentsQuery.data?.length,
      isLoading: instrumentsQuery.isLoading,
      isError: instrumentsQuery.isError,
      href: '/reference-data/instruments',
      icon: <Tag className="h-4 w-4" />,
    },
    {
      title: 'Account Types',
      description: 'Account type registry with behavior classes and CEL policy configuration.',
      count: accountTypesQuery.data?.length,
      isLoading: accountTypesQuery.isLoading,
      isError: accountTypesQuery.isError,
      href: '/reference-data/account-types',
      icon: <Layers className="h-4 w-4" />,
    },
    {
      title: 'Nodes',
      description: 'Hierarchical reference data nodes with bi-temporal query support.',
      count: nodesQuery.data?.length,
      isLoading: nodesQuery.isLoading,
      isError: nodesQuery.isError,
      href: '/reference-data/nodes',
      icon: <GitBranch className="h-4 w-4" />,
    },
  ]

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Reference Data</h1>
        <p className="mt-2 text-muted-foreground">
          Manage instruments, account types, and hierarchical reference data nodes.
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        {cards.map((card) => (
          <ReferenceDataCard key={card.href} {...card} />
        ))}
      </div>
    </div>
  )
}
