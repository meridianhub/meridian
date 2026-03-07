import { DEFAULT_MAX_CHAIN_DEPTH } from '@/lib/visualization/constants'
import { analyzeFilter, type EventContext, type FilterResult } from './cel-filter-analyzer'
import { analyzeSagaOutputs, type ProducedEvent as StarParserProducedEvent } from '@/lib/visualization/star-parser'
import type { ManifestGraph, ManifestNode, SagaTriggerMetadata } from './manifest-graph-model'

export interface ProducedEvent {
  channel: string
  instrumentCode: string | null
  accountId: string | null
  direction: 'DEBIT' | 'CREDIT' | null
}

export interface EventHop {
  depth: number
  trigger: ProducedEvent
  saga: string
  filterExpression: string | undefined
  filterResult: FilterResult
  filterReason: string
  producedEvents: ProducedEvent[]
}

export interface EventChain {
  hops: EventHop[]
  terminationReason: 'filter_rejection' | 'chain_depth_limit' | 'no_matching_sagas'
  maxDepthUsed: number
}

function buildEventContext(event: ProducedEvent): EventContext {
  const ctx: EventContext = {}
  if (event.instrumentCode) ctx.instrumentCode = event.instrumentCode
  if (event.direction) ctx.direction = event.direction
  return ctx
}

function sagaSourceFromNode(node: ManifestNode): string | null {
  const source = node.data['source']
  return typeof source === 'string' ? source : null
}

function producedEventsFromSagaOutputs(
  node: ManifestNode,
): ProducedEvent[] {
  const source = sagaSourceFromNode(node)
  if (!source) return []

  const analysis = analyzeSagaOutputs(source)
  return analysis.producedEvents.map((pe: StarParserProducedEvent) => ({
    channel: `position-keeping.transaction-captured.v1`,
    instrumentCode: pe.instrumentCode,
    accountId: pe.accountId,
    direction: pe.direction,
  }))
}

function findMatchingSagas(
  graph: ManifestGraph,
  event: ProducedEvent,
): ManifestNode[] {
  return graph.nodes.filter((node) => {
    if (node.type !== 'saga') return false
    if (!node.triggerMetadata) return false
    return node.triggerMetadata.channel === event.channel
  })
}

function generateInitialEvents(node: ManifestNode): ProducedEvent[] {
  if (node.type === 'instrument') {
    const code = node.data['code'] as string | undefined
    return [{
      channel: 'position-keeping.transaction-captured.v1',
      instrumentCode: code ?? null,
      accountId: null,
      direction: null,
    }]
  }

  if (node.type === 'account_type') {
    return [{
      channel: 'position-keeping.transaction-captured.v1',
      instrumentCode: null,
      accountId: null,
      direction: null,
    }]
  }

  return []
}

export function computeTransitiveClosure(
  graph: ManifestGraph,
  startNodeId: string,
  maxDepth: number = DEFAULT_MAX_CHAIN_DEPTH,
): EventChain {
  const startNode = graph.nodes.find((n) => n.id === startNodeId)
  if (!startNode) {
    return { hops: [], terminationReason: 'no_matching_sagas', maxDepthUsed: 0 }
  }

  let currentEvents = generateInitialEvents(startNode)
  if (currentEvents.length === 0) {
    return { hops: [], terminationReason: 'no_matching_sagas', maxDepthUsed: 0 }
  }

  const hops: EventHop[] = []
  let maxDepthUsed = 0

  for (let depth = 1; depth <= maxDepth; depth++) {
    const nextEvents: ProducedEvent[] = []
    let anyMatch = false
    let allFiltered = true

    for (const event of currentEvents) {
      const matchingSagas = findMatchingSagas(graph, event)
      if (matchingSagas.length === 0) continue

      anyMatch = true

      for (const sagaNode of matchingSagas) {
        const trigger = sagaNode.triggerMetadata as SagaTriggerMetadata
        const filterExpr = trigger.filterExpression
        const ctx = buildEventContext(event)
        const filterAnalysis = analyzeFilter(filterExpr ?? '', ctx)

        if (filterAnalysis.result === 'fail') {
          hops.push({
            depth,
            trigger: event,
            saga: sagaNode.id,
            filterExpression: filterExpr,
            filterResult: filterAnalysis.result,
            filterReason: filterAnalysis.reason,
            producedEvents: [],
          })
          continue
        }

        allFiltered = false
        const produced = producedEventsFromSagaOutputs(sagaNode)

        hops.push({
          depth,
          trigger: event,
          saga: sagaNode.id,
          filterExpression: filterExpr,
          filterResult: filterAnalysis.result,
          filterReason: filterAnalysis.reason,
          producedEvents: produced,
        })

        nextEvents.push(...produced)
      }
    }

    maxDepthUsed = depth

    if (!anyMatch) {
      return { hops, terminationReason: 'no_matching_sagas', maxDepthUsed }
    }

    if (allFiltered) {
      return { hops, terminationReason: 'filter_rejection', maxDepthUsed }
    }

    if (nextEvents.length === 0) {
      return { hops, terminationReason: 'no_matching_sagas', maxDepthUsed }
    }

    if (depth === maxDepth) {
      return { hops, terminationReason: 'chain_depth_limit', maxDepthUsed }
    }

    currentEvents = nextEvents
  }

  return { hops, terminationReason: 'no_matching_sagas', maxDepthUsed }
}
