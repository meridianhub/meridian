import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

export type ManifestNodeType = 'instrument' | 'account_type' | 'valuation_rule' | 'saga'

export interface SagaTriggerMetadata {
  channel: string
  filterExpression?: string
}

export interface ManifestNode {
  id: string
  type: ManifestNodeType
  label: string
  data: Record<string, unknown>
  triggerMetadata?: SagaTriggerMetadata
  dynamicTargets?: string[]
}

export type ManifestRelationship =
  | 'allowed_by'
  | 'converts_from'
  | 'converts_to'

export interface ManifestEdge {
  id: string
  source: string
  target: string
  relationship: ManifestRelationship
}

export interface ManifestGraph {
  nodes: ManifestNode[]
  edges: ManifestEdge[]
}

function parseSagaTrigger(trigger: string): SagaTriggerMetadata | undefined {
  if (!trigger.startsWith('event:')) return undefined

  const rest = trigger.slice('event:'.length)
  const pipeIndex = rest.indexOf('|')

  if (pipeIndex === -1) {
    return { channel: rest }
  }

  return {
    channel: rest.slice(0, pipeIndex),
    filterExpression: rest.slice(pipeIndex + 1),
  }
}

export function buildManifestGraph(manifest: Manifest): ManifestGraph {
  const nodes: ManifestNode[] = []
  const edges: ManifestEdge[] = []

  const instruments = (manifest as Record<string, unknown>).instruments as Array<Record<string, unknown>> ?? []
  const accountTypes = (manifest as Record<string, unknown>).accountTypes as Array<Record<string, unknown>> ?? []
  const valuationRules = (manifest as Record<string, unknown>).valuationRules as Array<Record<string, unknown>> ?? []
  const sagas = (manifest as Record<string, unknown>).sagas as Array<Record<string, unknown>> ?? []

  for (const inst of instruments) {
    nodes.push({
      id: `instrument:${inst.code}`,
      type: 'instrument',
      label: inst.name as string,
      data: { ...inst },
    })
  }

  for (const at of accountTypes) {
    nodes.push({
      id: `account_type:${at.code}`,
      type: 'account_type',
      label: at.name as string,
      data: { ...at },
    })

    const allowed = at.allowedInstruments as string[] | undefined
    if (allowed) {
      for (const instrumentCode of allowed) {
        edges.push({
          id: `allowed_by:${at.code}:${instrumentCode}`,
          source: `account_type:${at.code}`,
          target: `instrument:${instrumentCode}`,
          relationship: 'allowed_by',
        })
      }
    }
  }

  for (const rule of valuationRules) {
    const from = rule.fromInstrument as string
    const to = rule.toInstrument as string
    const ruleId = `valuation_rule:${from}:${to}`

    nodes.push({
      id: ruleId,
      type: 'valuation_rule',
      label: `${from} -> ${to}`,
      data: { ...rule },
    })

    edges.push({
      id: `converts_from:${from}:${to}`,
      source: ruleId,
      target: `instrument:${from}`,
      relationship: 'converts_from',
    })

    edges.push({
      id: `converts_to:${from}:${to}`,
      source: ruleId,
      target: `instrument:${to}`,
      relationship: 'converts_to',
    })
  }

  for (const saga of sagas) {
    const name = saga.name as string
    const trigger = saga.trigger as string
    const triggerMetadata = parseSagaTrigger(trigger)

    nodes.push({
      id: `saga:${name}`,
      type: 'saga',
      label: name,
      data: { ...saga },
      triggerMetadata,
    })
  }

  return { nodes, edges }
}
