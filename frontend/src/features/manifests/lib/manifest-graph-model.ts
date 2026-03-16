import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { analyzeSagaOutputs } from '@/lib/visualization/star-parser'

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
  script?: string
  [key: string]: unknown
}

interface ManifestPaymentRail {
  provider: string
  [key: string]: unknown
}

interface ManifestPartyType {
  partyType: string
  [key: string]: unknown
}

interface ManifestMapping {
  name: string
  [key: string]: unknown
}

interface ManifestProviderConnection {
  connectionId: string
  providerName: string
  [key: string]: unknown
}

interface ManifestInstructionRoute {
  instructionType: string
  connectionId: string
  fallbackConnectionId?: string
  outboundMappingId?: string
  inboundMappingId?: string
  [key: string]: unknown
}

interface ManifestOperationalGateway {
  providerConnections?: ManifestProviderConnection[]
  instructionRoutes?: ManifestInstructionRoute[]
  [key: string]: unknown
}

interface ManifestInput {
  instruments?: ManifestInstrument[]
  accountTypes?: ManifestAccountType[]
  valuationRules?: ManifestValuationRule[]
  sagas?: ManifestSaga[]
  paymentRails?: ManifestPaymentRail[]
  partyTypes?: ManifestPartyType[]
  mappings?: ManifestMapping[]
  operationalGateway?: ManifestOperationalGateway
}

export type ManifestNodeType =
  | 'instrument'
  | 'account_type'
  | 'valuation_rule'
  | 'saga'
  | 'payment_rail'
  | 'party_type'
  | 'mapping'
  | 'operational_gateway'
  | 'provider_connection'
  | 'instruction_route'
  | 'event_channel'

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
  dynamicTargets?: { variableName: string; codeSnippet: string }[]
}

export type ManifestRelationship =
  | 'allowed_by'
  | 'converts_from'
  | 'converts_to'
  | 'writes_to'
  | 'uses_valuation'
  | 'belongs_to'
  | 'routes_to'
  | 'fallback_to'
  | 'uses_mapping'
  | 'triggered_by'
  | 'produces'

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
  const paymentRails = m.paymentRails ?? []
  const partyTypes = m.partyTypes ?? []
  const mappings = m.mappings ?? []
  const operationalGateway = m.operationalGateway

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

  // Build lookup: instrument code -> account type codes that allow it
  const instrumentToAccountTypes = new Map<string, string[]>()
  for (const at of accountTypes) {
    if (at.allowedInstruments) {
      for (const ic of at.allowedInstruments) {
        const list = instrumentToAccountTypes.get(ic) ?? []
        list.push(at.code)
        instrumentToAccountTypes.set(ic, list)
      }
    }
  }

  for (const saga of sagas) {
    const { name, trigger, filter, script } = saga
    const triggerMetadata = parseSagaTrigger(trigger, filter)
    const sagaNodeId = `saga:${name}`

    const sagaNode: ManifestNode = {
      id: sagaNodeId,
      type: 'saga',
      label: name,
      data: { ...saga },
      triggerMetadata,
    }

    if (script) {
      const outputs = analyzeSagaOutputs(script)

      // writes_to edges: saga -> account types that allow the produced instrument
      for (const event of outputs.producedEvents) {
        if (event.instrumentCode) {
          const targetAccountTypes = instrumentToAccountTypes.get(event.instrumentCode) ?? []
          for (const accountTypeCode of targetAccountTypes) {
            edges.push({
              id: `writes_to:${sagaNodeId}:${accountTypeCode}:${event.stepName}`,
              source: sagaNodeId,
              target: `account_type:${accountTypeCode}`,
              relationship: 'writes_to',
            })
          }
        }
      }

      // uses_valuation edges: saga -> matching valuation rules
      for (const vc of outputs.valuationCalls) {
        if (vc.fromInstrument && vc.toInstrument) {
          for (let i = 0; i < valuationRules.length; i++) {
            const rule = valuationRules[i]
            if (rule.fromInstrument === vc.fromInstrument && rule.toInstrument === vc.toInstrument) {
              const ruleId = `valuation_rule:${rule.fromInstrument}:${rule.toInstrument}:${i}`
              edges.push({
                id: `uses_valuation:${sagaNodeId}:${ruleId}`,
                source: sagaNodeId,
                target: ruleId,
                relationship: 'uses_valuation',
              })
            }
          }
        }
      }

      // Record dynamic targets
      if (outputs.dynamicTargets.length > 0) {
        sagaNode.dynamicTargets = outputs.dynamicTargets.map((dt) => ({
          variableName: dt.variableName,
          codeSnippet: dt.codeSnippet,
        }))
      }
    }

    nodes.push(sagaNode)
  }

  // Event channel virtual nodes and edges
  const eventChannels = new Set<string>()

  // Collect channels from saga triggers
  for (const saga of sagas) {
    if (saga.trigger?.startsWith('event:')) {
      eventChannels.add(saga.trigger.slice('event:'.length))
    }
  }

  // Collect channels from saga outputs (position_keeping.initiate_log produces on this channel)
  const sagaProducedChannels = new Map<string, Set<string>>()
  for (const saga of sagas) {
    if (saga.script) {
      const outputs = analyzeSagaOutputs(saga.script)
      if (outputs.producedEvents.length > 0) {
        const channel = 'position-keeping.transaction-captured.v1'
        eventChannels.add(channel)
        const channels = sagaProducedChannels.get(saga.name) ?? new Set<string>()
        channels.add(channel)
        sagaProducedChannels.set(saga.name, channels)
      }
    }
  }

  // Create event_channel nodes
  for (const channel of eventChannels) {
    nodes.push({
      id: `event_channel:${channel}`,
      type: 'event_channel',
      label: channel,
      data: { channel },
    })
  }

  // Create triggered_by edges: saga -> event_channel (saga listens on this channel)
  for (const saga of sagas) {
    if (saga.trigger?.startsWith('event:')) {
      const channel = saga.trigger.slice('event:'.length)
      edges.push({
        id: `triggered_by:${saga.name}:${channel}`,
        source: `saga:${saga.name}`,
        target: `event_channel:${channel}`,
        relationship: 'triggered_by',
      })
    }
  }

  // Create produces edges: saga -> event_channel (saga emits events on this channel)
  for (const [sagaName, channels] of sagaProducedChannels) {
    for (const channel of channels) {
      edges.push({
        id: `produces:${sagaName}:${channel}`,
        source: `saga:${sagaName}`,
        target: `event_channel:${channel}`,
        relationship: 'produces',
      })
    }
  }

  // Payment rails
  for (const rail of paymentRails) {
    nodes.push({
      id: `payment_rail:${rail.provider}`,
      type: 'payment_rail',
      label: rail.provider,
      data: { ...rail },
    })
  }

  // Party types
  for (const pt of partyTypes) {
    nodes.push({
      id: `party_type:${pt.partyType}`,
      type: 'party_type',
      label: pt.partyType,
      data: { ...pt },
    })
  }

  // Mappings
  const mappingNames = new Set<string>()
  for (const mapping of mappings) {
    mappingNames.add(mapping.name)
    nodes.push({
      id: `mapping:${mapping.name}`,
      type: 'mapping',
      label: mapping.name,
      data: { ...mapping },
    })
  }

  // Operational gateway and its children
  if (operationalGateway) {
    const gwNodeId = 'operational_gateway:default'
    nodes.push({
      id: gwNodeId,
      type: 'operational_gateway',
      label: 'Operational Gateway',
      data: { ...operationalGateway },
    })

    const providerConnections = operationalGateway.providerConnections ?? []
    const connectionIds = new Set(providerConnections.map((c) => c.connectionId))

    for (const conn of providerConnections) {
      const connNodeId = `provider_connection:${conn.connectionId}`
      nodes.push({
        id: connNodeId,
        type: 'provider_connection',
        label: conn.providerName,
        data: { ...conn },
      })

      edges.push({
        id: `belongs_to:${conn.connectionId}:${gwNodeId}`,
        source: connNodeId,
        target: gwNodeId,
        relationship: 'belongs_to',
      })
    }

    const instructionRoutes = operationalGateway.instructionRoutes ?? []
    for (const route of instructionRoutes) {
      const routeNodeId = `instruction_route:${route.instructionType}`
      nodes.push({
        id: routeNodeId,
        type: 'instruction_route',
        label: route.instructionType,
        data: { ...route },
      })

      // routes_to: instruction route -> primary provider connection
      if (connectionIds.has(route.connectionId)) {
        edges.push({
          id: `routes_to:${route.instructionType}:${route.connectionId}`,
          source: routeNodeId,
          target: `provider_connection:${route.connectionId}`,
          relationship: 'routes_to',
        })
      }

      // fallback_to: instruction route -> fallback provider connection
      if (route.fallbackConnectionId && connectionIds.has(route.fallbackConnectionId)) {
        edges.push({
          id: `fallback_to:${route.instructionType}:${route.fallbackConnectionId}`,
          source: routeNodeId,
          target: `provider_connection:${route.fallbackConnectionId}`,
          relationship: 'fallback_to',
        })
      }

      // uses_mapping: instruction route -> outbound mapping
      if (route.outboundMappingId && mappingNames.has(route.outboundMappingId)) {
        edges.push({
          id: `uses_mapping:${route.instructionType}:outbound:${route.outboundMappingId}`,
          source: routeNodeId,
          target: `mapping:${route.outboundMappingId}`,
          relationship: 'uses_mapping',
        })
      }

      // uses_mapping: instruction route -> inbound mapping
      if (route.inboundMappingId && mappingNames.has(route.inboundMappingId)) {
        edges.push({
          id: `uses_mapping:${route.instructionType}:inbound:${route.inboundMappingId}`,
          source: routeNodeId,
          target: `mapping:${route.inboundMappingId}`,
          relationship: 'uses_mapping',
        })
      }
    }
  }

  return { nodes, edges }
}
