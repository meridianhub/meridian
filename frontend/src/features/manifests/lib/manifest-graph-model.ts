import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

interface ManifestInstrument {
  code: string
  name: string
  [key: string]: unknown
}

interface ManifestAccountType {
  code: string
  name: string
  allowedInstruments?: string[]
  [key: string]: unknown
}

interface ManifestValuationRule {
  fromInstrument: string
  toInstrument: string
  method: number
  [key: string]: unknown
}

interface ManifestSaga {
  name: string
  trigger: string
  filter?: string
  [key: string]: unknown
}

interface ManifestInput {
  instruments?: ManifestInstrument[]
  accountTypes?: ManifestAccountType[]
  valuationRules?: ManifestValuationRule[]
  sagas?: ManifestSaga[]
}

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

function parseSagaTrigger(
  trigger: string,
  filter?: string,
): SagaTriggerMetadata | undefined {
  if (!trigger.startsWith('event:')) return undefined

  const channel = trigger.slice('event:'.length)
  return {
    channel,
    ...(filter ? { filterExpression: filter } : {}),
  }
}

export function buildManifestGraph(manifest: Manifest): ManifestGraph {
  const nodes: ManifestNode[] = []
  const edges: ManifestEdge[] = []

  const m = manifest as unknown as ManifestInput
  const instruments = m.instruments ?? []
  const accountTypes = m.accountTypes ?? []
  const valuationRules = m.valuationRules ?? []
  const sagas = m.sagas ?? []

  const instrumentCodes = new Set(instruments.map((i) => i.code))

  for (const inst of instruments) {
    nodes.push({
      id: `instrument:${inst.code}`,
      type: 'instrument',
      label: inst.name,
      data: { ...inst },
    })
  }

  for (const at of accountTypes) {
    nodes.push({
      id: `account_type:${at.code}`,
      type: 'account_type',
      label: at.name,
      data: { ...at },
    })

    if (at.allowedInstruments) {
      for (const instrumentCode of at.allowedInstruments) {
        if (instrumentCodes.has(instrumentCode)) {
          edges.push({
            id: `allowed_by:${at.code}:${instrumentCode}`,
            source: `account_type:${at.code}`,
            target: `instrument:${instrumentCode}`,
            relationship: 'allowed_by',
          })
        }
      }
    }
  }

  for (let i = 0; i < valuationRules.length; i++) {
    const rule = valuationRules[i]
    const { fromInstrument: from, toInstrument: to } = rule
    const ruleId = `valuation_rule:${from}:${to}:${i}`

    nodes.push({
      id: ruleId,
      type: 'valuation_rule',
      label: `${from} -> ${to}`,
      data: { ...rule },
    })

    if (instrumentCodes.has(from)) {
      edges.push({
        id: `converts_from:${from}:${to}:${i}`,
        source: ruleId,
        target: `instrument:${from}`,
        relationship: 'converts_from',
      })
    }

    if (instrumentCodes.has(to)) {
      edges.push({
        id: `converts_to:${from}:${to}:${i}`,
        source: ruleId,
        target: `instrument:${to}`,
        relationship: 'converts_to',
      })
    }
  }

  for (const saga of sagas) {
    const { name, trigger, filter } = saga
    const triggerMetadata = parseSagaTrigger(trigger, filter)

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
