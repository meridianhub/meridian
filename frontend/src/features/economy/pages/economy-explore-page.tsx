import { useQuery } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import type { SagaDefinition } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import type { MappingDefinition } from '@/api/gen/meridian/mapping/v1/mapping_pb'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Card, CardContent } from '@/components/ui/card'
import { Breadcrumbs } from '@/shared/breadcrumbs'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { ResourcesPanel } from '@/features/economy/components/resources-panel'
import { GatewayPanel } from '@/features/economy/components/gateway-panel'
import { ConfigPanel } from '@/features/economy/components/config-panel'

// ── Loading / empty / error states ────────────────────────────────────────────

function LoadingSkeleton() {
  return (
    <div data-testid="explorer-loading" className="p-6 space-y-4">
      <Skeleton className="h-8 w-64" />
      <Skeleton className="h-10 w-96" />
      <Skeleton className="h-64 w-full" />
    </div>
  )
}

function EmptyState() {
  return (
    <div data-testid="explorer-empty" className="p-6 flex flex-col items-center gap-4 py-16 text-muted-foreground">
      <span className="text-lg font-medium">No custom economy configured</span>
      <span className="text-sm text-center max-w-md">
        Your tenant includes 28 platform capabilities out of the box - 8 sagas, 12 account types,
        5 valuation methods, and 3 policies. Apply a manifest to add custom configurations.
      </span>
      <div className="flex flex-wrap gap-2 mt-2 text-sm">
        <a href="/starlark-config" className="text-primary hover:underline">View Sagas</a>
        <span className="text-muted-foreground">·</span>
        <a href="/reference-data/account-types" className="text-primary hover:underline">View Account Types</a>
        <span className="text-muted-foreground">·</span>
        <a href="/reference-data/valuation-rules" className="text-primary hover:underline">View Valuation Rules</a>
      </div>
    </div>
  )
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  return (
    <div data-testid="explorer-error" className="p-6 flex flex-col items-center gap-3 py-16 text-muted-foreground">
      <span className="text-sm font-medium">Unable to load economy</span>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

// ── EventChannelsPanel ─────────────────────────────────────────────────────────

interface EventChannel {
  channel: string
  sagas: SagaDefinition[]
}

function buildEventChannels(sagas: SagaDefinition[]): EventChannel[] {
  const channelMap = new Map<string, SagaDefinition[]>()

  for (const saga of sagas) {
    if (saga.trigger.startsWith('event:')) {
      const channel = saga.trigger.slice('event:'.length)
      const existing = channelMap.get(channel) ?? []
      existing.push(saga)
      channelMap.set(channel, existing)
    }
  }

  return Array.from(channelMap.entries()).map(([channel, boundSagas]) => ({
    channel,
    sagas: boundSagas,
  }))
}

function EventChannelsPanel({ sagas }: { sagas: SagaDefinition[] }) {
  const channels = buildEventChannels(sagas)

  if (channels.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground text-sm">
        No event channels defined. Create sagas with <code className="text-xs">event:</code> triggers to see them here.
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {channels.map((ch) => (
        <Card key={ch.channel}>
          <CardContent className="flex items-center justify-between px-4 py-3">
            <span className="font-mono text-sm font-medium text-foreground">{ch.channel}</span>
            <Badge className="bg-success-muted text-success-foreground hover:bg-success-muted">
              {ch.sagas.length} saga{ch.sagas.length === 1 ? '' : 's'} attached
            </Badge>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

// ── SagasPanel ─────────────────────────────────────────────────────────────────

function SagasPanel({ sagas }: { sagas: SagaDefinition[] }) {
  if (sagas.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground text-sm">
        No sagas defined in this manifest.
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {sagas.map((saga) => (
        <Card key={saga.name}>
          <CardContent className="px-4 py-3 space-y-1">
            <div className="flex items-center justify-between">
              <span className="font-mono text-sm font-medium">{saga.name}</span>
              {saga.trigger.startsWith('event:') ? (
                <Badge variant="secondary">event-driven</Badge>
              ) : (
                <Badge variant="outline">{saga.trigger.split(':')[0]}</Badge>
              )}
            </div>
            <p className="text-xs text-muted-foreground font-mono">{saga.trigger}</p>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

// ── Mappings panel ────────────────────────────────────────────────────────────

function ApiEndpointsPanel({ mappings }: { mappings: MappingDefinition[] }) {
  if (mappings.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground text-sm">
        No API mappings defined in this manifest.
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {mappings.map((mapping) => (
        <Card key={mapping.name}>
          <CardContent className="flex items-center justify-between px-4 py-3">
            <div className="space-y-0.5">
              <span className="font-mono text-sm font-medium">{mapping.name}</span>
              {mapping.targetService && (
                <p className="text-xs text-muted-foreground font-mono">{mapping.targetService}</p>
              )}
            </div>
            {mapping.targetRpc && (
              <Badge variant="secondary">{mapping.targetRpc}</Badge>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

// ── Page ───────────────────────────────────────────────────────────────────────

export function EconomyExplorePage() {
  const { manifestHistory } = useApiClients()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })

  const isNotFound = error instanceof ConnectError && error.code === Code.NotFound

  const content = (() => {
    if (isLoading && !data) return <LoadingSkeleton />
    if (error && !isNotFound && !data) return <ErrorState onRetry={() => void refetch()} />
    if (isNotFound || !data?.version?.manifest) return <EmptyState />

    const manifest: Manifest = data.version.manifest
    const version = data.version
    const sagas = manifest.sagas ?? []
    const mappings = manifest.mappings ?? []

    return (
      <>
        <div>
          <h1 className="text-2xl font-semibold">Economy Explorer</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Explore event channels, sagas, API mappings, and resources in your economy.
          </p>
        </div>

        <Tabs defaultValue="event-channels">
          <TabsList>
            <TabsTrigger value="event-channels">Event Channels</TabsTrigger>
            <TabsTrigger value="sagas">Sagas</TabsTrigger>
            <TabsTrigger value="api-endpoints">API Endpoints</TabsTrigger>
            <TabsTrigger value="resources">Resources</TabsTrigger>
            <TabsTrigger value="gateway">Gateway</TabsTrigger>
            <TabsTrigger value="config">Config</TabsTrigger>
          </TabsList>

          <TabsContent value="event-channels" className="mt-4">
            <EventChannelsPanel sagas={sagas} />
          </TabsContent>

          <TabsContent value="sagas" className="mt-4">
            <SagasPanel sagas={sagas} />
          </TabsContent>

          <TabsContent value="api-endpoints" className="mt-4">
            <ApiEndpointsPanel mappings={mappings} />
          </TabsContent>

          <TabsContent value="resources" className="mt-4">
            <ResourcesPanel manifest={manifest} />
          </TabsContent>

          <TabsContent value="gateway" className="mt-4">
            <GatewayPanel gateway={manifest.operationalGateway} />
          </TabsContent>

          <TabsContent value="config" className="mt-4">
            <ConfigPanel version={version} />
          </TabsContent>
        </Tabs>
      </>
    )
  })()

  return (
    <div className="p-6 space-y-6">
      <Breadcrumbs items={[{ label: 'Economy', href: '/economy' }, { label: 'Explore' }]} />
      {content}
    </div>
  )
}
