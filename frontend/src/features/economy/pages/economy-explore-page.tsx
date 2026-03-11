import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import { buildManifestGraph } from '@/features/manifests/lib/manifest-graph-model'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Card, CardContent } from '@/components/ui/card'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

// ── Types ──────────────────────────────────────────────────────────────────────

interface ManifestSaga {
  name: string
  trigger: string
  filter?: string
  script?: string
  [key: string]: unknown
}

interface ManifestHandler {
  name: string
  module?: string
  path?: string
  [key: string]: unknown
}

interface ManifestInstrument {
  code: string
  name: string
  [key: string]: unknown
}

interface ManifestAccountType {
  code: string
  name: string
  [key: string]: unknown
}

interface ManifestInput {
  sagas?: ManifestSaga[]
  handlers?: ManifestHandler[]
  instruments?: ManifestInstrument[]
  accountTypes?: ManifestAccountType[]
}

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
      <span className="text-lg font-medium">No economy configured</span>
      <span className="text-sm text-center max-w-md">
        Apply a manifest to configure instruments, sagas, event channels, and more.
      </span>
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
  boundSagas: ManifestSaga[]
}

function buildEventChannels(sagas: ManifestSaga[]): EventChannel[] {
  const channelMap = new Map<string, ManifestSaga[]>()

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
    boundSagas,
  }))
}

function EventChannelsPanel({ manifest }: { manifest: Manifest }) {
  const m = manifest as unknown as ManifestInput
  const sagas = m.sagas ?? []
  const channels = buildEventChannels(sagas)

  if (channels.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground text-sm">
        No event channels defined. Create sagas with <code className="text-xs">event:</code> triggers to see them here.
      </div>
    )
  }

  const bound = channels.filter((c) => c.boundSagas.length > 0)
  const unbound = channels.filter((c) => c.boundSagas.length === 0)

  return (
    <div className="space-y-4">
      {bound.length > 0 && (
        <section>
          <h3 className="text-sm font-medium text-muted-foreground mb-2">Bound Channels</h3>
          <div className="space-y-2">
            {bound.map((ch) => (
              <Card key={ch.channel}>
                <CardContent className="flex items-center justify-between px-4 py-3">
                  <div className="flex items-center gap-3">
                    <span className="font-mono text-sm font-medium text-foreground">{ch.channel}</span>
                  </div>
                  <Badge className="bg-green-100 text-green-800 hover:bg-green-100 dark:bg-green-900 dark:text-green-200">
                    {ch.boundSagas.length} saga attached
                  </Badge>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}

      {unbound.length > 0 && (
        <section>
          <h3 className="text-sm font-medium text-muted-foreground mb-2">Unbound Channels</h3>
          <div className="space-y-2">
            {unbound.map((ch) => (
              <Card key={ch.channel}>
                <CardContent className="flex items-center justify-between px-4 py-3">
                  <span className="font-mono text-sm font-medium text-foreground">{ch.channel}</span>
                  <Button size="sm" variant="outline">
                    Add Saga
                  </Button>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

// ── SagasPanel ─────────────────────────────────────────────────────────────────

function SagasPanel({ manifest }: { manifest: Manifest }) {
  // Use buildManifestGraph to extract saga nodes (consistent with graph view)
  const graph = buildManifestGraph(manifest)
  const sagaNodes = graph.nodes.filter((n) => n.type === 'saga')
  const m = manifest as unknown as ManifestInput
  const sagas = m.sagas ?? []

  if (sagaNodes.length === 0) {
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
            {saga.filter && (
              <p className="text-xs text-muted-foreground">
                Filter: <code className="text-xs">{saga.filter}</code>
              </p>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

// ── API Endpoints panel ────────────────────────────────────────────────────────

function ApiEndpointsPanel({ manifest }: { manifest: Manifest }) {
  const m = manifest as unknown as ManifestInput
  const handlers = m.handlers ?? []

  if (handlers.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground text-sm">
        No API endpoints defined in this manifest.
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {handlers.map((handler) => (
        <Card key={handler.name}>
          <CardContent className="flex items-center justify-between px-4 py-3">
            <div className="space-y-0.5">
              <span className="font-mono text-sm font-medium">{handler.name}</span>
              {handler.path && (
                <p className="text-xs text-muted-foreground font-mono">{handler.path}</p>
              )}
            </div>
            {handler.module && (
              <Badge variant="secondary">{handler.module}</Badge>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

// ── ResourcesPanel ────────────────────────────────────────────────────────────

function ResourcesPanel({ manifest }: { manifest: Manifest }) {
  const m = manifest as unknown as ManifestInput
  const instruments = m.instruments ?? []
  const accountTypes = m.accountTypes ?? []

  if (instruments.length === 0 && accountTypes.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground text-sm">
        No instruments or account types defined in this manifest.
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {instruments.length > 0 && (
        <section>
          <h3 className="text-sm font-medium text-muted-foreground mb-2">Instruments</h3>
          <div className="space-y-2">
            {instruments.map((inst) => (
              <Card key={inst.code}>
                <CardContent className="flex items-center justify-between px-4 py-3">
                  <span className="text-sm font-medium">{inst.name}</span>
                  <Badge variant="outline" className="font-mono">{inst.code}</Badge>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}

      {accountTypes.length > 0 && (
        <section>
          <h3 className="text-sm font-medium text-muted-foreground mb-2">Account Types</h3>
          <div className="space-y-2">
            {accountTypes.map((at) => (
              <Card key={at.code}>
                <CardContent className="flex items-center justify-between px-4 py-3">
                  <span className="text-sm font-medium">{at.name}</span>
                  <Badge variant="outline" className="font-mono">{at.code}</Badge>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}
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

  if (isLoading) return <LoadingSkeleton />
  if (error) return <ErrorState onRetry={() => void refetch()} />
  if (!data?.version?.manifest) return <EmptyState />

  const { manifest } = data.version

  return (
    <div className="p-6 space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Economy Explorer</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Explore event channels, sagas, API endpoints, and resources in your economy.
        </p>
      </div>

      <Tabs defaultValue="event-channels">
        <TabsList>
          <TabsTrigger value="event-channels">Event Channels</TabsTrigger>
          <TabsTrigger value="sagas">Sagas</TabsTrigger>
          <TabsTrigger value="api-endpoints">API Endpoints</TabsTrigger>
          <TabsTrigger value="resources">Resources</TabsTrigger>
        </TabsList>

        <TabsContent value="event-channels" className="mt-4">
          <EventChannelsPanel manifest={manifest} />
        </TabsContent>

        <TabsContent value="sagas" className="mt-4">
          <SagasPanel manifest={manifest} />
        </TabsContent>

        <TabsContent value="api-endpoints" className="mt-4">
          <ApiEndpointsPanel manifest={manifest} />
        </TabsContent>

        <TabsContent value="resources" className="mt-4">
          <ResourcesPanel manifest={manifest} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
