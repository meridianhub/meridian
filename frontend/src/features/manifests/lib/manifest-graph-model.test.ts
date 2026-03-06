import { describe, it, expect } from 'vitest'
import { buildManifestGraph } from './manifest-graph-model'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

function createMockManifest(overrides: Partial<Record<string, unknown>> = {}): Manifest {
  return {
    version: '1.0',
    metadata: { name: 'Test Manifest', industry: 'energy', description: '' },
    instruments: [],
    accountTypes: [],
    valuationRules: [],
    sagas: [],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
    operationalGateway: undefined,
    ...overrides,
  } as unknown as Manifest
}

const energyManifest = createMockManifest({
  instruments: [
    { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
    { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
  ],
  accountTypes: [
    {
      code: 'ENERGY_HOLDING',
      name: 'Energy Holding',
      normalBalance: 1,
      allowedInstruments: ['KWH'],
      policies: undefined,
    },
    {
      code: 'REVENUE',
      name: 'Revenue Account',
      normalBalance: 2,
      allowedInstruments: ['GBP', 'KWH'],
      policies: undefined,
    },
  ],
  valuationRules: [
    { fromInstrument: 'KWH', toInstrument: 'GBP', method: 1, source: 'nordpool_spot' },
  ],
  sagas: [
    {
      name: 'usage_to_value',
      trigger: 'event:position-keeping.transaction-captured.v1|event.instrument_code == "KWH"',
      script: 'def main(): pass',
    },
    {
      name: 'daily_reconciliation',
      trigger: 'scheduled:daily_reconciliation',
      script: 'def main(): pass',
    },
    {
      name: 'process_payment',
      trigger: 'api:/v1/payments',
      script: 'def main(): pass',
    },
  ],
})

describe('buildManifestGraph', () => {
  describe('instrument nodes', () => {
    it('creates a node for each instrument', () => {
      const graph = buildManifestGraph(energyManifest)
      const instrumentNodes = graph.nodes.filter((n) => n.type === 'instrument')

      expect(instrumentNodes).toHaveLength(2)
      expect(instrumentNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'instrument:KWH', label: 'Kilowatt Hour' }),
          expect.objectContaining({ id: 'instrument:GBP', label: 'British Pound' }),
        ]),
      )
    })

    it('includes instrument data in node', () => {
      const graph = buildManifestGraph(energyManifest)
      const kwh = graph.nodes.find((n) => n.id === 'instrument:KWH')

      expect(kwh?.data).toMatchObject({ code: 'KWH', name: 'Kilowatt Hour' })
    })
  })

  describe('account type nodes', () => {
    it('creates a node for each account type', () => {
      const graph = buildManifestGraph(energyManifest)
      const atNodes = graph.nodes.filter((n) => n.type === 'account_type')

      expect(atNodes).toHaveLength(2)
      expect(atNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'account_type:ENERGY_HOLDING', label: 'Energy Holding' }),
          expect.objectContaining({ id: 'account_type:REVENUE', label: 'Revenue Account' }),
        ]),
      )
    })
  })

  describe('allowed_by edges', () => {
    it('creates edges from account types to their allowed instruments', () => {
      const graph = buildManifestGraph(energyManifest)
      const allowedEdges = graph.edges.filter((e) => e.relationship === 'allowed_by')

      expect(allowedEdges).toHaveLength(3)
      expect(allowedEdges).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            source: 'account_type:ENERGY_HOLDING',
            target: 'instrument:KWH',
            relationship: 'allowed_by',
          }),
          expect.objectContaining({
            source: 'account_type:REVENUE',
            target: 'instrument:GBP',
            relationship: 'allowed_by',
          }),
          expect.objectContaining({
            source: 'account_type:REVENUE',
            target: 'instrument:KWH',
            relationship: 'allowed_by',
          }),
        ]),
      )
    })

    it('creates no edges when allowedInstruments is empty', () => {
      const manifest = createMockManifest({
        accountTypes: [
          { code: 'OPEN', name: 'Open Account', normalBalance: 1, allowedInstruments: [] },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const allowedEdges = graph.edges.filter((e) => e.relationship === 'allowed_by')

      expect(allowedEdges).toHaveLength(0)
    })
  })

  describe('valuation rule nodes and edges', () => {
    it('creates a node for each valuation rule', () => {
      const graph = buildManifestGraph(energyManifest)
      const ruleNodes = graph.nodes.filter((n) => n.type === 'valuation_rule')

      expect(ruleNodes).toHaveLength(1)
      expect(ruleNodes[0]).toMatchObject({
        id: 'valuation_rule:KWH:GBP',
        label: 'KWH -> GBP',
      })
    })

    it('creates converts_from and converts_to edges', () => {
      const graph = buildManifestGraph(energyManifest)

      const fromEdges = graph.edges.filter((e) => e.relationship === 'converts_from')
      const toEdges = graph.edges.filter((e) => e.relationship === 'converts_to')

      expect(fromEdges).toHaveLength(1)
      expect(fromEdges[0]).toMatchObject({
        source: 'valuation_rule:KWH:GBP',
        target: 'instrument:KWH',
      })

      expect(toEdges).toHaveLength(1)
      expect(toEdges[0]).toMatchObject({
        source: 'valuation_rule:KWH:GBP',
        target: 'instrument:GBP',
      })
    })
  })

  describe('saga nodes', () => {
    it('creates a node for each saga', () => {
      const graph = buildManifestGraph(energyManifest)
      const sagaNodes = graph.nodes.filter((n) => n.type === 'saga')

      expect(sagaNodes).toHaveLength(3)
    })

    it('parses event trigger with filter into triggerMetadata', () => {
      const graph = buildManifestGraph(energyManifest)
      const usage = graph.nodes.find((n) => n.id === 'saga:usage_to_value')

      expect(usage?.triggerMetadata).toEqual({
        channel: 'position-keeping.transaction-captured.v1',
        filterExpression: 'event.instrument_code == "KWH"',
      })
    })

    it('parses event trigger without filter', () => {
      const manifest = createMockManifest({
        sagas: [{ name: 'all_events', trigger: 'event:some.channel.v1', script: '' }],
      })
      const graph = buildManifestGraph(manifest)
      const node = graph.nodes.find((n) => n.id === 'saga:all_events')

      expect(node?.triggerMetadata).toEqual({
        channel: 'some.channel.v1',
      })
    })

    it('does not set triggerMetadata for non-event triggers', () => {
      const graph = buildManifestGraph(energyManifest)
      const scheduled = graph.nodes.find((n) => n.id === 'saga:daily_reconciliation')
      const api = graph.nodes.find((n) => n.id === 'saga:process_payment')

      expect(scheduled?.triggerMetadata).toBeUndefined()
      expect(api?.triggerMetadata).toBeUndefined()
    })
  })

  describe('edge cases', () => {
    it('returns empty graph for empty manifest', () => {
      const manifest = createMockManifest()
      const graph = buildManifestGraph(manifest)

      expect(graph.nodes).toHaveLength(0)
      expect(graph.edges).toHaveLength(0)
    })

    it('handles manifest with instruments but no relationships', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'USD', name: 'US Dollar', type: 1, dimensions: { unit: 'USD', precision: 2 } },
        ],
      })
      const graph = buildManifestGraph(manifest)

      expect(graph.nodes).toHaveLength(1)
      expect(graph.edges).toHaveLength(0)
    })
  })
})
