import { useMemo } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useManifestGraph } from '@/features/manifests/hooks/use-manifest-graph'
import { useEventChain } from '@/features/manifests/hooks/use-event-chain'
import { EventChainPanel } from '@/features/manifests/components/event-chain-panel'
import type { ManifestGraph, ManifestNode } from '@/features/manifests/lib/manifest-graph-model'

interface ExecutionContextTabProps {
  entityType: 'instrument' | 'account_type'
  entityCode: string
}

function findRelatedSagas(graph: ManifestGraph, nodeId: string): ManifestNode[] {
  const sagaIds = new Set<string>()

  for (const edge of graph.edges) {
    if (edge.relationship === 'writes_to') {
      if (edge.source.startsWith('saga:') && edge.target === nodeId) {
        sagaIds.add(edge.source)
      }
    }
    if (edge.relationship === 'allowed_by') {
      if (edge.source === nodeId || edge.target === nodeId) {
        const otherNodeId = edge.source === nodeId ? edge.target : edge.source
        for (const e2 of graph.edges) {
          if (e2.relationship === 'writes_to' && e2.source.startsWith('saga:') && e2.target === otherNodeId) {
            sagaIds.add(e2.source)
          }
          if (e2.relationship === 'writes_to' && e2.source.startsWith('saga:') && e2.target === nodeId) {
            sagaIds.add(e2.source)
          }
        }
      }
    }
  }

  return graph.nodes.filter((n) => sagaIds.has(n.id))
}

function findRelatedValuationRules(graph: ManifestGraph, instrumentCode: string): ManifestNode[] {
  const instrumentNodeId = `instrument:${instrumentCode}`
  const ruleIds = new Set<string>()

  for (const edge of graph.edges) {
    if (
      (edge.relationship === 'converts_from' || edge.relationship === 'converts_to') &&
      edge.target === instrumentNodeId
    ) {
      ruleIds.add(edge.source)
    }
  }

  return graph.nodes.filter((n) => ruleIds.has(n.id))
}

export function ExecutionContextTab({ entityType, entityCode }: ExecutionContextTabProps) {
  const { graph, isLoading, error } = useManifestGraph()

  const nodeId = `${entityType}:${entityCode}`

  const relatedSagas = useMemo(
    () => (graph ? findRelatedSagas(graph, nodeId) : []),
    [graph, nodeId],
  )

  const relatedValuationRules = useMemo(
    () => (graph && entityType === 'instrument' ? findRelatedValuationRules(graph, entityCode) : []),
    [graph, entityType, entityCode],
  )

  const node = useMemo(
    () => graph?.nodes.find((n) => n.id === nodeId) ?? null,
    [graph, nodeId],
  )

  const eventChain = useEventChain(graph, nodeId)

  if (isLoading) {
    return (
      <div className="py-8 text-center text-sm text-muted-foreground">
        Loading execution context...
      </div>
    )
  }

  if (error) {
    return (
      <div className="py-8 text-center text-sm text-destructive">
        Failed to load manifest data.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Related Sagas</CardTitle>
        </CardHeader>
        <CardContent>
          {relatedSagas.length === 0 ? (
            <p className="text-sm text-muted-foreground">No related sagas found.</p>
          ) : (
            <ul className="space-y-2">
              {relatedSagas.map((saga) => (
                <li key={saga.id} className="flex items-center gap-2 rounded border px-3 py-2">
                  <span className="text-sm font-medium">{saga.label}</span>
                  {saga.triggerMetadata && (
                    <span className="text-xs text-muted-foreground">
                      trigger: {saga.triggerMetadata.channel}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      {entityType === 'instrument' && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Valuation Rules</CardTitle>
          </CardHeader>
          <CardContent>
            {relatedValuationRules.length === 0 ? (
              <p className="text-sm text-muted-foreground">No valuation rules reference this instrument.</p>
            ) : (
              <ul className="space-y-2">
                {relatedValuationRules.map((rule) => (
                  <li key={rule.id} className="flex items-center gap-2 rounded border px-3 py-2">
                    <span className="text-sm font-medium">{rule.label}</span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      )}

      {eventChain && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Event Chain</CardTitle>
          </CardHeader>
          <CardContent>
            <EventChainPanel
              chain={eventChain}
              startNodeLabel={node?.label ?? entityCode}
            />
          </CardContent>
        </Card>
      )}
    </div>
  )
}
