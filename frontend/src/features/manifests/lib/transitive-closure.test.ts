import { describe, it, expect } from 'vitest'
import { computeTransitiveClosure } from './transitive-closure'
import type { ManifestGraph, ManifestNode } from './manifest-graph-model'

function makeInstrumentNode(code: string): ManifestNode {
  return {
    id: `instrument:${code}`,
    type: 'instrument',
    label: code,
    data: { code },
  }
}

function makeAccountTypeNode(code: string): ManifestNode {
  return {
    id: `account_type:${code}`,
    type: 'account_type',
    label: code,
    data: { code },
  }
}

function makeSagaNode(
  name: string,
  channel: string,
  opts: {
    filter?: string
    source?: string
  } = {},
): ManifestNode {
  return {
    id: `saga:${name}`,
    type: 'saga',
    label: name,
    data: {
      name,
      trigger: `event:${channel}`,
      ...(opts.filter ? { filter: opts.filter } : {}),
      ...(opts.source ? { source: opts.source } : {}),
    },
    triggerMetadata: {
      channel,
      ...(opts.filter ? { filterExpression: opts.filter } : {}),
    },
  }
}

const SAGA_SOURCE_KWH_TO_GBP = `
# Saga: usage_to_value
# Trigger: event:position-keeping.transaction-captured.v1
# Filter: event.instrument_code == 'KWH'

saga(name="usage_to_value")

step(name="convert")
    position_keeping.initiate_log(
        instrument_code="GBP",
        account_id="revenue",
        direction="CREDIT",
    )
`

const SAGA_SOURCE_GBP_POSTING = `
# Saga: gbp_posting
# Trigger: event:position-keeping.transaction-captured.v1
# Filter: event.instrument_code == 'GBP'

saga(name="gbp_posting")

step(name="post")
    position_keeping.initiate_log(
        instrument_code="EUR",
        account_id="fx",
        direction="DEBIT",
    )
`

const SAGA_SOURCE_NO_OUTPUTS = `
# Saga: audit_only
# Trigger: event:position-keeping.transaction-captured.v1

saga(name="audit_only")

step(name="log")
    audit.record(entity_id="123")
`

describe('computeTransitiveClosure', () => {
  it('returns empty chain for non-existent start node', () => {
    const graph: ManifestGraph = { nodes: [], edges: [] }
    const result = computeTransitiveClosure(graph, 'instrument:MISSING')

    expect(result.hops).toHaveLength(0)
    expect(result.terminationReason).toBe('no_matching_sagas')
    expect(result.maxDepthUsed).toBe(0)
  })

  it('terminates with no_matching_sagas when no saga matches the channel', () => {
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('KWH'),
        makeSagaNode('unrelated', 'some-other-channel.v1'),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:KWH')

    expect(result.hops).toHaveLength(0)
    expect(result.terminationReason).toBe('no_matching_sagas')
    expect(result.maxDepthUsed).toBe(1)
  })

  it('computes single-hop chain: instrument -> saga -> termination', () => {
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('KWH'),
        makeSagaNode('usage_to_value', 'position-keeping.transaction-captured.v1', {
          filter: "event.instrument_code == 'KWH'",
          source: SAGA_SOURCE_KWH_TO_GBP,
        }),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:KWH')

    expect(result.hops.length).toBeGreaterThanOrEqual(1)

    const firstHop = result.hops[0]
    expect(firstHop.depth).toBe(1)
    expect(firstHop.saga).toBe('saga:usage_to_value')
    expect(firstHop.filterResult).toBe('pass')
    expect(firstHop.producedEvents).toHaveLength(1)
    expect(firstHop.producedEvents[0].instrumentCode).toBe('GBP')
    expect(firstHop.producedEvents[0].direction).toBe('CREDIT')
  })

  it('computes multi-hop chain: KWH -> usage_to_value -> GBP -> gbp_posting -> EUR', () => {
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('KWH'),
        makeSagaNode('usage_to_value', 'position-keeping.transaction-captured.v1', {
          filter: "event.instrument_code == 'KWH'",
          source: SAGA_SOURCE_KWH_TO_GBP,
        }),
        makeSagaNode('gbp_posting', 'position-keeping.transaction-captured.v1', {
          filter: "event.instrument_code == 'GBP'",
          source: SAGA_SOURCE_GBP_POSTING,
        }),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:KWH')

    // Depth 1: KWH triggers usage_to_value (pass) and gbp_posting (fail on KWH != GBP)
    const depth1Pass = result.hops.find(
      (h) => h.depth === 1 && h.saga === 'saga:usage_to_value',
    )
    expect(depth1Pass).toBeDefined()
    expect(depth1Pass!.filterResult).toBe('pass')
    expect(depth1Pass!.producedEvents[0].instrumentCode).toBe('GBP')

    const depth1Fail = result.hops.find(
      (h) => h.depth === 1 && h.saga === 'saga:gbp_posting',
    )
    expect(depth1Fail).toBeDefined()
    expect(depth1Fail!.filterResult).toBe('fail')

    // Depth 2: GBP triggers gbp_posting (pass) and usage_to_value (fail on GBP != KWH)
    const depth2Pass = result.hops.find(
      (h) => h.depth === 2 && h.saga === 'saga:gbp_posting',
    )
    expect(depth2Pass).toBeDefined()
    expect(depth2Pass!.filterResult).toBe('pass')
    expect(depth2Pass!.producedEvents[0].instrumentCode).toBe('EUR')
  })

  it('terminates on filter_rejection when all sagas reject the event', () => {
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('USD'),
        makeSagaNode('kwh_only', 'position-keeping.transaction-captured.v1', {
          filter: "event.instrument_code == 'KWH'",
          source: SAGA_SOURCE_KWH_TO_GBP,
        }),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:USD')

    expect(result.terminationReason).toBe('filter_rejection')
    expect(result.hops).toHaveLength(1)
    expect(result.hops[0].filterResult).toBe('fail')
    expect(result.maxDepthUsed).toBe(1)
  })

  it('terminates on chain_depth_limit', () => {
    // Create a saga that always passes (no filter) and produces events back on the same channel
    const selfLoopSource = `
saga(name="echo")
step(name="echo_step")
    position_keeping.initiate_log(
        instrument_code="LOOP",
        account_id="loop_account",
        direction="DEBIT",
    )
`
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('LOOP'),
        makeSagaNode('echo', 'position-keeping.transaction-captured.v1', {
          source: selfLoopSource,
        }),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:LOOP', 3)

    expect(result.terminationReason).toBe('chain_depth_limit')
    expect(result.maxDepthUsed).toBe(3)
    expect(result.hops.length).toBe(3)
  })

  it('uses DEFAULT_MAX_CHAIN_DEPTH when maxDepth not specified', () => {
    // Just verify it doesn't crash with default - use a graph that terminates early
    const graph: ManifestGraph = {
      nodes: [makeInstrumentNode('X')],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:X')
    expect(result.terminationReason).toBe('no_matching_sagas')
  })

  it('handles energy-settlement pattern: KWH -> usage_to_value -> GBP terminates because GBP fails filter', () => {
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('KWH'),
        makeSagaNode('usage_to_value', 'position-keeping.transaction-captured.v1', {
          filter: "event.instrument_code == 'KWH'",
          source: SAGA_SOURCE_KWH_TO_GBP,
        }),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:KWH')

    // Depth 1: KWH matches filter, produces GBP event
    expect(result.hops[0].filterResult).toBe('pass')
    expect(result.hops[0].producedEvents[0].instrumentCode).toBe('GBP')

    // Depth 2: GBP fails the KWH filter -> filter_rejection
    expect(result.terminationReason).toBe('filter_rejection')
    expect(result.maxDepthUsed).toBe(2)
  })

  it('starts from account_type node with generic event', () => {
    // Account type produces event without instrumentCode, so filter is indeterminate
    const graph: ManifestGraph = {
      nodes: [
        makeAccountTypeNode('OPERATING'),
        makeSagaNode('usage_to_value', 'position-keeping.transaction-captured.v1', {
          filter: "event.instrument_code == 'KWH'",
          source: SAGA_SOURCE_KWH_TO_GBP,
        }),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'account_type:OPERATING')

    // Filter should be indeterminate since account_type event has no instrumentCode
    const hop = result.hops.find((h) => h.depth === 1)
    expect(hop).toBeDefined()
    expect(hop!.filterResult).toBe('indeterminate')
    expect(hop!.producedEvents).toHaveLength(1)
  })

  it('terminates with no_matching_sagas when saga has no outputs', () => {
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('KWH'),
        makeSagaNode('audit_only', 'position-keeping.transaction-captured.v1', {
          source: SAGA_SOURCE_NO_OUTPUTS,
        }),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:KWH')

    expect(result.hops).toHaveLength(1)
    expect(result.hops[0].producedEvents).toHaveLength(0)
    expect(result.terminationReason).toBe('no_matching_sagas')
  })

  it('handles saga node without source gracefully', () => {
    const graph: ManifestGraph = {
      nodes: [
        makeInstrumentNode('KWH'),
        makeSagaNode('no_source', 'position-keeping.transaction-captured.v1'),
      ],
      edges: [],
    }

    const result = computeTransitiveClosure(graph, 'instrument:KWH')

    expect(result.hops).toHaveLength(1)
    expect(result.hops[0].producedEvents).toHaveLength(0)
    expect(result.terminationReason).toBe('no_matching_sagas')
  })
})
