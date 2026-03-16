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

  describe('payment rail nodes', () => {
    it('creates a node for each payment rail', () => {
      const manifest = createMockManifest({
        paymentRails: [
          { provider: 'stripe_connect', mode: 1, accountId: 'acct_ABC123456789012345', webhookEndpointSecret: 'whsec_test', platformFee: { type: 1, value: '2.5' }, payoutSchedule: 1, supportedMethods: ['card'] },
          { provider: 'wise', mode: 2, accountId: 'acct_XYZ987654321098765', webhookEndpointSecret: 'whsec_prod', platformFee: { type: 2, value: '1.00' }, payoutSchedule: 2, supportedMethods: ['bank_transfer'] },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const railNodes = graph.nodes.filter((n) => n.type === 'payment_rail')

      expect(railNodes).toHaveLength(2)
      expect(railNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'payment_rail:stripe_connect', label: 'stripe_connect' }),
          expect.objectContaining({ id: 'payment_rail:wise', label: 'wise' }),
        ]),
      )
    })
  })

  describe('party type nodes', () => {
    it('creates a node for each party type', () => {
      const manifest = createMockManifest({
        partyTypes: [
          { id: 'pt-1', tenantId: 't-1', partyType: 'PERSON', attributeSchema: '{}' },
          { id: 'pt-2', tenantId: 't-1', partyType: 'ORGANIZATION', attributeSchema: '{}' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const ptNodes = graph.nodes.filter((n) => n.type === 'party_type')

      expect(ptNodes).toHaveLength(2)
      expect(ptNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'party_type:PERSON', label: 'PERSON' }),
          expect.objectContaining({ id: 'party_type:ORGANIZATION', label: 'ORGANIZATION' }),
        ]),
      )
    })
  })

  describe('mapping nodes', () => {
    it('creates a node for each mapping definition', () => {
      const manifest = createMockManifest({
        mappings: [
          { id: 'm-1', tenantId: 't-1', name: 'Stripe Webhook -> Payment Order', targetService: 'payment_order.v1' },
          { id: 'm-2', tenantId: 't-1', name: 'KYC Response -> Party Update', targetService: 'party.v1' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const mappingNodes = graph.nodes.filter((n) => n.type === 'mapping')

      expect(mappingNodes).toHaveLength(2)
      expect(mappingNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'mapping:Stripe Webhook -> Payment Order', label: 'Stripe Webhook -> Payment Order' }),
          expect.objectContaining({ id: 'mapping:KYC Response -> Party Update', label: 'KYC Response -> Party Update' }),
        ]),
      )
    })
  })

  describe('operational gateway nodes', () => {
    it('creates an operational_gateway node when present', () => {
      const manifest = createMockManifest({
        operationalGateway: {
          providerConnections: [],
          instructionRoutes: [],
          inboundRoutes: [],
        },
      })
      const graph = buildManifestGraph(manifest)
      const gwNodes = graph.nodes.filter((n) => n.type === 'operational_gateway')

      expect(gwNodes).toHaveLength(1)
      expect(gwNodes[0]).toMatchObject({
        id: 'operational_gateway:default',
        label: 'Operational Gateway',
      })
    })

    it('does not create operational_gateway node when absent', () => {
      const manifest = createMockManifest({
        operationalGateway: undefined,
      })
      const graph = buildManifestGraph(manifest)
      const gwNodes = graph.nodes.filter((n) => n.type === 'operational_gateway')

      expect(gwNodes).toHaveLength(0)
    })
  })

  describe('provider connection nodes and edges', () => {
    it('creates a node for each provider connection', () => {
      const manifest = createMockManifest({
        operationalGateway: {
          providerConnections: [
            { connectionId: 'stripe-primary', providerName: 'Stripe', providerType: 'payment_gateway', protocol: 1, baseUrl: 'https://api.stripe.com' },
            { connectionId: 'kyc-provider', providerName: 'Onfido', providerType: 'kyc_provider', protocol: 1, baseUrl: 'https://api.onfido.com' },
          ],
          instructionRoutes: [],
          inboundRoutes: [],
        },
      })
      const graph = buildManifestGraph(manifest)
      const connNodes = graph.nodes.filter((n) => n.type === 'provider_connection')

      expect(connNodes).toHaveLength(2)
      expect(connNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'provider_connection:stripe-primary', label: 'Stripe' }),
          expect.objectContaining({ id: 'provider_connection:kyc-provider', label: 'Onfido' }),
        ]),
      )
    })

    it('creates belongs_to edges from provider connections to operational gateway', () => {
      const manifest = createMockManifest({
        operationalGateway: {
          providerConnections: [
            { connectionId: 'stripe-primary', providerName: 'Stripe', providerType: 'payment_gateway', protocol: 1, baseUrl: 'https://api.stripe.com' },
          ],
          instructionRoutes: [],
          inboundRoutes: [],
        },
      })
      const graph = buildManifestGraph(manifest)
      const belongsToEdges = graph.edges.filter((e) => e.relationship === 'belongs_to')

      expect(belongsToEdges).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            source: 'provider_connection:stripe-primary',
            target: 'operational_gateway:default',
            relationship: 'belongs_to',
          }),
        ]),
      )
    })
  })

  describe('instruction route nodes and edges', () => {
    it('creates a node for each instruction route', () => {
      const manifest = createMockManifest({
        operationalGateway: {
          providerConnections: [
            { connectionId: 'stripe-primary', providerName: 'Stripe', providerType: 'payment_gateway', protocol: 1, baseUrl: 'https://api.stripe.com' },
          ],
          instructionRoutes: [
            { instructionType: 'payment.initiate', connectionId: 'stripe-primary', fallbackConnectionId: '', outboundMappingId: 'stripe-outbound', inboundMappingId: '', httpMethod: 'POST', pathTemplate: '/v1/charges' },
            { instructionType: 'kyc.verify', connectionId: 'stripe-primary', fallbackConnectionId: '', outboundMappingId: '', inboundMappingId: '', httpMethod: 'POST', pathTemplate: '/v1/identity' },
          ],
          inboundRoutes: [],
        },
      })
      const graph = buildManifestGraph(manifest)
      const routeNodes = graph.nodes.filter((n) => n.type === 'instruction_route')

      expect(routeNodes).toHaveLength(2)
      expect(routeNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'instruction_route:payment.initiate', label: 'payment.initiate' }),
          expect.objectContaining({ id: 'instruction_route:kyc.verify', label: 'kyc.verify' }),
        ]),
      )
    })

    it('creates routes_to edges from instruction routes to provider connections', () => {
      const manifest = createMockManifest({
        operationalGateway: {
          providerConnections: [
            { connectionId: 'stripe-primary', providerName: 'Stripe', providerType: 'payment_gateway', protocol: 1, baseUrl: 'https://api.stripe.com' },
          ],
          instructionRoutes: [
            { instructionType: 'payment.initiate', connectionId: 'stripe-primary', fallbackConnectionId: '', outboundMappingId: '', inboundMappingId: '', httpMethod: 'POST', pathTemplate: '/v1/charges' },
          ],
          inboundRoutes: [],
        },
      })
      const graph = buildManifestGraph(manifest)
      const routesToEdges = graph.edges.filter((e) => e.relationship === 'routes_to')

      expect(routesToEdges).toHaveLength(1)
      expect(routesToEdges[0]).toMatchObject({
        source: 'instruction_route:payment.initiate',
        target: 'provider_connection:stripe-primary',
        relationship: 'routes_to',
      })
    })

    it('creates uses_mapping edges from instruction routes to mappings', () => {
      const manifest = createMockManifest({
        mappings: [
          { id: 'm-1', tenantId: 't-1', name: 'stripe-outbound', targetService: 'payment_order.v1' },
          { id: 'm-2', tenantId: 't-1', name: 'stripe-inbound', targetService: 'payment_order.v1' },
        ],
        operationalGateway: {
          providerConnections: [
            { connectionId: 'stripe-primary', providerName: 'Stripe', providerType: 'payment_gateway', protocol: 1, baseUrl: 'https://api.stripe.com' },
          ],
          instructionRoutes: [
            { instructionType: 'payment.initiate', connectionId: 'stripe-primary', fallbackConnectionId: '', outboundMappingId: 'stripe-outbound', inboundMappingId: 'stripe-inbound', httpMethod: 'POST', pathTemplate: '/v1/charges' },
          ],
          inboundRoutes: [],
        },
      })
      const graph = buildManifestGraph(manifest)
      const usesMappingEdges = graph.edges.filter((e) => e.relationship === 'uses_mapping')

      expect(usesMappingEdges).toHaveLength(2)
      expect(usesMappingEdges).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            source: 'instruction_route:payment.initiate',
            target: 'mapping:stripe-outbound',
            relationship: 'uses_mapping',
          }),
          expect.objectContaining({
            source: 'instruction_route:payment.initiate',
            target: 'mapping:stripe-inbound',
            relationship: 'uses_mapping',
          }),
        ]),
      )
    })

    it('creates fallback_to edges when fallback connection is set', () => {
      const manifest = createMockManifest({
        operationalGateway: {
          providerConnections: [
            { connectionId: 'stripe-primary', providerName: 'Stripe', providerType: 'payment_gateway', protocol: 1, baseUrl: 'https://api.stripe.com' },
            { connectionId: 'stripe-backup', providerName: 'Stripe Backup', providerType: 'payment_gateway', protocol: 1, baseUrl: 'https://api.stripe.com' },
          ],
          instructionRoutes: [
            { instructionType: 'payment.initiate', connectionId: 'stripe-primary', fallbackConnectionId: 'stripe-backup', outboundMappingId: '', inboundMappingId: '', httpMethod: 'POST', pathTemplate: '/v1/charges' },
          ],
          inboundRoutes: [],
        },
      })
      const graph = buildManifestGraph(manifest)
      const fallbackEdges = graph.edges.filter((e) => e.relationship === 'fallback_to')

      expect(fallbackEdges).toHaveLength(1)
      expect(fallbackEdges[0]).toMatchObject({
        source: 'instruction_route:payment.initiate',
        target: 'provider_connection:stripe-backup',
        relationship: 'fallback_to',
      })
    })
  })

  describe('event_channel nodes', () => {
    it('creates event_channel nodes from saga event triggers', () => {
      const manifest = createMockManifest({
        sagas: [
          { name: 'saga_a', trigger: 'event:order.created.v1', script: '' },
          { name: 'saga_b', trigger: 'event:payment.completed.v1', script: '' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const channelNodes = graph.nodes.filter((n) => n.type === 'event_channel')

      expect(channelNodes).toHaveLength(2)
      expect(channelNodes).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: 'event_channel:order.created.v1', label: 'order.created.v1' }),
          expect.objectContaining({ id: 'event_channel:payment.completed.v1', label: 'payment.completed.v1' }),
        ]),
      )
    })

    it('deduplicates event_channel nodes when multiple sagas listen on the same channel', () => {
      const manifest = createMockManifest({
        sagas: [
          { name: 'saga_a', trigger: 'event:order.created.v1', script: '' },
          { name: 'saga_b', trigger: 'event:order.created.v1', filter: 'event.type == "PREMIUM"', script: '' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const channelNodes = graph.nodes.filter((n) => n.type === 'event_channel')

      expect(channelNodes).toHaveLength(1)
      expect(channelNodes[0]).toMatchObject({ id: 'event_channel:order.created.v1' })
    })

    it('does not create event_channel nodes for non-event triggers', () => {
      const manifest = createMockManifest({
        sagas: [
          { name: 'saga_a', trigger: 'scheduled:daily_check', script: '' },
          { name: 'saga_b', trigger: 'api:/v1/payments', script: '' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const channelNodes = graph.nodes.filter((n) => n.type === 'event_channel')

      expect(channelNodes).toHaveLength(0)
    })

    it('creates event_channel node for channel produced by saga outputs', () => {
      const manifest = createMockManifest({
        sagas: [{
          name: 'log_energy',
          trigger: 'api:/v1/energy',
          script: [
            'saga(name="log_energy")',
            'step(name="record")',
            'position_keeping.initiate_log(instrument_code="KWH", direction="CREDIT", amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const channelNodes = graph.nodes.filter((n) => n.type === 'event_channel')

      expect(channelNodes).toHaveLength(1)
      expect(channelNodes[0]).toMatchObject({
        id: 'event_channel:position-keeping.transaction-captured.v1',
        label: 'position-keeping.transaction-captured.v1',
      })
    })
  })

  describe('triggered_by edges', () => {
    it('creates triggered_by edges from saga to event_channel it listens on', () => {
      const manifest = createMockManifest({
        sagas: [
          { name: 'saga_a', trigger: 'event:order.created.v1', script: '' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const triggeredBy = graph.edges.filter((e) => e.relationship === 'triggered_by')

      expect(triggeredBy).toHaveLength(1)
      expect(triggeredBy[0]).toMatchObject({
        source: 'saga:saga_a',
        target: 'event_channel:order.created.v1',
        relationship: 'triggered_by',
      })
    })

    it('creates triggered_by edges for multiple sagas on same channel', () => {
      const manifest = createMockManifest({
        sagas: [
          { name: 'saga_a', trigger: 'event:order.created.v1', script: '' },
          { name: 'saga_b', trigger: 'event:order.created.v1', filter: 'event.type == "X"', script: '' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const triggeredBy = graph.edges.filter((e) => e.relationship === 'triggered_by')

      expect(triggeredBy).toHaveLength(2)
      expect(triggeredBy).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ source: 'saga:saga_a', target: 'event_channel:order.created.v1' }),
          expect.objectContaining({ source: 'saga:saga_b', target: 'event_channel:order.created.v1' }),
        ]),
      )
    })

    it('does not create triggered_by edges for non-event triggers', () => {
      const manifest = createMockManifest({
        sagas: [
          { name: 'saga_a', trigger: 'scheduled:daily_check', script: '' },
        ],
      })
      const graph = buildManifestGraph(manifest)
      const triggeredBy = graph.edges.filter((e) => e.relationship === 'triggered_by')

      expect(triggeredBy).toHaveLength(0)
    })
  })

  describe('produces edges', () => {
    it('creates produces edges from saga to event_channel when saga emits events', () => {
      const manifest = createMockManifest({
        sagas: [{
          name: 'log_energy',
          trigger: 'api:/v1/energy',
          script: [
            'saga(name="log_energy")',
            'step(name="record")',
            'position_keeping.initiate_log(instrument_code="KWH", direction="CREDIT", amount=qty)',
          ].join('\n'),
        }],
      })
      const graph = buildManifestGraph(manifest)
      const produces = graph.edges.filter((e) => e.relationship === 'produces')

      expect(produces).toHaveLength(1)
      expect(produces[0]).toMatchObject({
        source: 'saga:log_energy',
        target: 'event_channel:position-keeping.transaction-captured.v1',
        relationship: 'produces',
      })
    })

    it('does not create produces edges for sagas without position_keeping calls', () => {
      const manifest = createMockManifest({
        sagas: [{
          name: 'simple_saga',
          trigger: 'event:order.created.v1',
          script: 'def main(): pass',
        }],
      })
      const graph = buildManifestGraph(manifest)
      const produces = graph.edges.filter((e) => e.relationship === 'produces')

      expect(produces).toHaveLength(0)
    })

    it('creates both triggered_by and produces edges showing full event chain', () => {
      const manifest = createMockManifest({
        sagas: [
          {
            name: 'record_usage',
            trigger: 'api:/v1/meter-reading',
            script: [
              'saga(name="record_usage")',
              'step(name="log")',
              'position_keeping.initiate_log(instrument_code="KWH", direction="CREDIT", amount=qty)',
            ].join('\n'),
          },
          {
            name: 'settle_usage',
            trigger: 'event:position-keeping.transaction-captured.v1',
            filter: 'event.instrument_code == "KWH"',
            script: 'def main(): pass',
          },
        ],
      })
      const graph = buildManifestGraph(manifest)

      // record_usage produces on the channel
      const produces = graph.edges.filter((e) => e.relationship === 'produces')
      expect(produces).toHaveLength(1)
      expect(produces[0]).toMatchObject({
        source: 'saga:record_usage',
        target: 'event_channel:position-keeping.transaction-captured.v1',
      })

      // settle_usage is triggered by the channel
      const triggeredBy = graph.edges.filter((e) => e.relationship === 'triggered_by')
      expect(triggeredBy).toHaveLength(1)
      expect(triggeredBy[0]).toMatchObject({
        source: 'saga:settle_usage',
        target: 'event_channel:position-keeping.transaction-captured.v1',
      })

      // Only one event_channel node (deduplicated)
      const channelNodes = graph.nodes.filter((n) => n.type === 'event_channel')
      expect(channelNodes).toHaveLength(1)
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
