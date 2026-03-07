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
      trigger: 'event:position-keeping.transaction-captured.v1',
      filter: 'event.instrument_code == "KWH"',
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
        id: 'valuation_rule:KWH:GBP:0',
        label: 'KWH -> GBP',
      })
    })

    it('creates converts_from and converts_to edges', () => {
      const graph = buildManifestGraph(energyManifest)

      const fromEdges = graph.edges.filter((e) => e.relationship === 'converts_from')
      const toEdges = graph.edges.filter((e) => e.relationship === 'converts_to')

      expect(fromEdges).toHaveLength(1)
      expect(fromEdges[0]).toMatchObject({
        source: 'valuation_rule:KWH:GBP:0',
        target: 'instrument:KWH',
      })

      expect(toEdges).toHaveLength(1)
      expect(toEdges[0]).toMatchObject({
        source: 'valuation_rule:KWH:GBP:0',
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

  describe('writes_to edges', () => {
    it('creates edges from saga to account types for static instrument_code', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
        ],
        accountTypes: [
          { code: 'ENERGY_HOLDING', name: 'Energy Holding', normalBalance: 1, allowedInstruments: ['KWH'] },
          { code: 'REVENUE', name: 'Revenue', normalBalance: 2, allowedInstruments: ['KWH'] },
        ],
        sagas: [{
          name: 'log_energy',
          trigger: 'event:meter.reading.v1',
          script: [
            'saga(name="log_energy")',
            'step(name="record")',
            'position_keeping.initiate_log(instrument_code="KWH", direction="CREDIT", amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const writesTo = graph.edges.filter((e) => e.relationship === 'writes_to')

      expect(writesTo).toHaveLength(2)
      expect(writesTo).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            source: 'saga:log_energy',
            target: 'account_type:ENERGY_HOLDING',
            relationship: 'writes_to',
          }),
          expect.objectContaining({
            source: 'saga:log_energy',
            target: 'account_type:REVENUE',
            relationship: 'writes_to',
          }),
        ]),
      )
    })

    it('creates no writes_to edges for fully dynamic sagas', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
        ],
        accountTypes: [
          { code: 'ENERGY_HOLDING', name: 'Energy Holding', normalBalance: 1, allowedInstruments: ['KWH'] },
        ],
        sagas: [{
          name: 'dynamic_saga',
          trigger: 'event:some.event.v1',
          script: [
            'saga(name="dynamic_saga")',
            'step(name="record")',
            'position_keeping.initiate_log(instrument_code=instr_var, direction="CREDIT", amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const writesTo = graph.edges.filter((e) => e.relationship === 'writes_to')

      expect(writesTo).toHaveLength(0)
    })

    it('creates unique edge IDs for multiple writes to same account type', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
        ],
        accountTypes: [
          { code: 'REVENUE', name: 'Revenue', normalBalance: 2, allowedInstruments: ['GBP'] },
        ],
        sagas: [{
          name: 'double_write',
          trigger: 'event:payment.v1',
          script: [
            'saga(name="double_write")',
            'step(name="debit")',
            'position_keeping.initiate_log(instrument_code="GBP", direction="DEBIT", amount=amt)',
            'step(name="credit")',
            'position_keeping.initiate_log(instrument_code="GBP", direction="CREDIT", amount=amt)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const writesTo = graph.edges.filter((e) => e.relationship === 'writes_to')

      expect(writesTo).toHaveLength(2)
      const ids = writesTo.map((e) => e.id)
      expect(new Set(ids).size).toBe(2)
    })
  })

  describe('uses_valuation edges', () => {
    it('creates edges from saga to matching valuation rules', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
          { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
        ],
        valuationRules: [
          { fromInstrument: 'KWH', toInstrument: 'GBP', method: 1, source: 'nordpool_spot' },
        ],
        sagas: [{
          name: 'convert_energy',
          trigger: 'event:meter.reading.v1',
          script: [
            'saga(name="convert_energy")',
            'step(name="valuate")',
            'valuation_engine.compute(from_instrument="KWH", to_instrument="GBP", amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const usesVal = graph.edges.filter((e) => e.relationship === 'uses_valuation')

      expect(usesVal).toHaveLength(1)
      expect(usesVal[0]).toMatchObject({
        source: 'saga:convert_energy',
        target: 'valuation_rule:KWH:GBP:0',
        relationship: 'uses_valuation',
      })
    })

    it('creates no uses_valuation edges when instruments are dynamic', () => {
      const manifest = createMockManifest({
        valuationRules: [
          { fromInstrument: 'KWH', toInstrument: 'GBP', method: 1, source: 'test' },
        ],
        sagas: [{
          name: 'dynamic_val',
          trigger: 'event:some.v1',
          script: [
            'saga(name="dynamic_val")',
            'step(name="valuate")',
            'valuation_engine.compute(from_instrument=from_var, to_instrument=to_var, amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const usesVal = graph.edges.filter((e) => e.relationship === 'uses_valuation')

      expect(usesVal).toHaveLength(0)
    })
  })

  describe('dynamic targets', () => {
    it('records dynamic targets when instrument_code is a variable', () => {
      const manifest = createMockManifest({
        sagas: [{
          name: 'dynamic_saga',
          trigger: 'event:some.v1',
          script: [
            'saga(name="dynamic_saga")',
            'step(name="record")',
            'position_keeping.initiate_log(instrument_code=instr_var, direction="CREDIT", amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const sagaNode = graph.nodes.find((n) => n.id === 'saga:dynamic_saga')

      expect(sagaNode?.dynamicTargets).toBeDefined()
      expect(sagaNode!.dynamicTargets!.length).toBeGreaterThan(0)
      expect(sagaNode!.dynamicTargets![0]).toMatchObject({
        variableName: 'instr_var',
      })
    })

    it('does not set dynamicTargets for static sagas', () => {
      const manifest = createMockManifest({
        sagas: [{
          name: 'static_saga',
          trigger: 'event:some.v1',
          script: [
            'saga(name="static_saga")',
            'step(name="record")',
            'position_keeping.initiate_log(instrument_code="GBP", direction="CREDIT", amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const sagaNode = graph.nodes.find((n) => n.id === 'saga:static_saga')

      expect(sagaNode?.dynamicTargets).toBeUndefined()
    })
  })

  describe('integration: energy-settlement manifest', () => {
    it('produces expected graph with writes_to and uses_valuation edges', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
          { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
        ],
        accountTypes: [
          { code: 'ENERGY_HOLDING', name: 'Energy Holding', normalBalance: 1, allowedInstruments: ['KWH'] },
          { code: 'REVENUE', name: 'Revenue', normalBalance: 2, allowedInstruments: ['GBP', 'KWH'] },
        ],
        valuationRules: [
          { fromInstrument: 'KWH', toInstrument: 'GBP', method: 1, source: 'nordpool_spot' },
        ],
        sagas: [{
          name: 'energy_settlement',
          trigger: 'event:meter.reading.v1',
          filter: 'event.type == "HH"',
          script: [
            'saga(name="energy_settlement")',
            'step(name="record_usage")',
            'position_keeping.initiate_log(instrument_code="KWH", direction="DEBIT", amount=usage)',
            'step(name="valuate")',
            'valuation_engine.compute(from_instrument="KWH", to_instrument="GBP", amount=usage)',
            'step(name="record_revenue")',
            'position_keeping.initiate_log(instrument_code="GBP", direction="CREDIT", amount=value)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)

      // Saga writes_to edges
      const writesTo = graph.edges.filter((e) => e.relationship === 'writes_to')
      // KWH -> ENERGY_HOLDING and REVENUE (step record_usage)
      // GBP -> REVENUE (step record_revenue)
      expect(writesTo).toHaveLength(3)
      expect(writesTo).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ target: 'account_type:ENERGY_HOLDING' }),
          expect.objectContaining({ target: 'account_type:REVENUE' }),
        ]),
      )

      // uses_valuation edge
      const usesVal = graph.edges.filter((e) => e.relationship === 'uses_valuation')
      expect(usesVal).toHaveLength(1)
      expect(usesVal[0]).toMatchObject({
        source: 'saga:energy_settlement',
        target: 'valuation_rule:KWH:GBP:0',
      })

      // No dynamic targets (all static)
      const sagaNode = graph.nodes.find((n) => n.id === 'saga:energy_settlement')
      expect(sagaNode?.dynamicTargets).toBeUndefined()
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

    it('skips allowed_by edges referencing nonexistent instruments', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
        ],
        accountTypes: [
          { code: 'MIXED', name: 'Mixed Account', normalBalance: 1, allowedInstruments: ['GBP', 'MISSING'] },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const allowedEdges = graph.edges.filter((e) => e.relationship === 'allowed_by')

      expect(allowedEdges).toHaveLength(1)
      expect(allowedEdges[0]).toMatchObject({ target: 'instrument:GBP' })
    })

    it('skips valuation edges referencing nonexistent instruments', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
        ],
        valuationRules: [
          { fromInstrument: 'MISSING', toInstrument: 'GBP', method: 1, source: 'test' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const fromEdges = graph.edges.filter((e) => e.relationship === 'converts_from')
      const toEdges = graph.edges.filter((e) => e.relationship === 'converts_to')

      expect(fromEdges).toHaveLength(0)
      expect(toEdges).toHaveLength(1)
    })

    it('assigns unique IDs to duplicate from/to valuation rules', () => {
      const manifest = createMockManifest({
        instruments: [
          { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
          { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
        ],
        valuationRules: [
          { fromInstrument: 'KWH', toInstrument: 'GBP', method: 1, source: 'nordpool_spot' },
          { fromInstrument: 'KWH', toInstrument: 'GBP', method: 2, source: 'admin_override' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const ruleNodes = graph.nodes.filter((n) => n.type === 'valuation_rule')

      expect(ruleNodes).toHaveLength(2)
      expect(ruleNodes[0].id).toBe('valuation_rule:KWH:GBP:0')
      expect(ruleNodes[1].id).toBe('valuation_rule:KWH:GBP:1')
    })
  })
})
