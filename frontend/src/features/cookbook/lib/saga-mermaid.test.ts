import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { join } from 'path'
import { generateMermaidMarkup } from './saga-mermaid'
import { parseStarlarkSaga, type SagaFlow } from './star-parser'

const PATTERNS_DIR = join(__dirname, '..', '..', '..', '..', '..', 'cookbook', 'patterns')

function readPattern(subdir: string, file: string): string {
  return readFileSync(join(PATTERNS_DIR, subdir, file), 'utf-8')
}

describe('generateMermaidMarkup', () => {
  it('generates valid flowchart header', () => {
    const flow: SagaFlow = {
      name: 'test_saga',
      trigger: null,
      filter: null,
      steps: [],
    }
    const markup = generateMermaidMarkup(flow)
    expect(markup).toMatch(/^flowchart TD/)
  })

  it('renders empty saga with start and end', () => {
    const flow: SagaFlow = {
      name: 'empty_saga',
      trigger: null,
      filter: null,
      steps: [],
    }
    const markup = generateMermaidMarkup(flow)
    expect(markup).toContain('START')
    expect(markup).toContain('END')
    expect(markup).toContain('empty_saga')
  })

  it('renders steps as rectangles', () => {
    const flow: SagaFlow = {
      name: 'test',
      trigger: null,
      filter: null,
      steps: [
        {
          name: 'step_one',
          lineNumber: 1,
          serviceCalls: [{ service: 'ref_data', method: 'get_account', params: ['id'] }],
          earlyExit: null,
        },
        {
          name: 'step_two',
          lineNumber: 5,
          serviceCalls: [{ service: 'pos_keeping', method: 'initiate_log', params: ['id'] }],
          earlyExit: null,
        },
      ],
    }
    const markup = generateMermaidMarkup(flow)
    expect(markup).toContain('S1["step_one\\nref_data.get_account"]')
    expect(markup).toContain('S2["step_two\\npos_keeping.initiate_log"]')
    expect(markup).toContain('S1 --> S2')
    expect(markup).toContain('S2 --> END')
  })

  it('renders trigger in start node', () => {
    const flow: SagaFlow = {
      name: 'test',
      trigger: 'event:pos.captured.v1',
      filter: null,
      steps: [
        { name: 's1', lineNumber: 1, serviceCalls: [], earlyExit: null },
      ],
    }
    const markup = generateMermaidMarkup(flow)
    expect(markup).toContain('event:pos.captured.v1')
  })

  it('renders early exits as decision diamonds', () => {
    const flow: SagaFlow = {
      name: 'test',
      trigger: null,
      filter: null,
      steps: [
        {
          name: 'check',
          lineNumber: 1,
          serviceCalls: [{ service: 'pk', method: 'query_logs', params: ['cid'] }],
          earlyExit: {
            condition: 'existing.count > 0',
            returnStatus: 'ALREADY_PROCESSED',
          },
        },
        {
          name: 'book',
          lineNumber: 5,
          serviceCalls: [{ service: 'pk', method: 'initiate_log', params: ['aid'] }],
          earlyExit: null,
        },
      ],
    }
    const markup = generateMermaidMarkup(flow)
    expect(markup).toContain('D1{')
    expect(markup).toContain('existing.count > 0')
    expect(markup).toContain('-->|Yes|')
    expect(markup).toContain('ALREADY_PROCESSED')
    expect(markup).toContain('-->|No| S2')
  })

  it('renders multiple service calls in one step', () => {
    const flow: SagaFlow = {
      name: 'test',
      trigger: null,
      filter: null,
      steps: [
        {
          name: 'multi',
          lineNumber: 1,
          serviceCalls: [
            { service: 'svc_a', method: 'do_x', params: [] },
            { service: 'svc_b', method: 'do_y', params: [] },
          ],
          earlyExit: null,
        },
      ],
    }
    const markup = generateMermaidMarkup(flow)
    expect(markup).toContain('multi\\nsvc_a.do_x\\nsvc_b.do_y')
  })

  describe('real pattern Mermaid output', () => {
    const patterns = [
      { dir: 'energy-settlement', file: 'usage_to_value.star' },
      { dir: 'dynamic-capacity-pricing', file: 'dynamic_capacity_billing.star' },
      { dir: 'entity-distribution', file: 'race_result_distribution.star' },
      { dir: 'kyc-compliance', file: 'kyc_on_party.star' },
      { dir: 'payment-gateway-stripe', file: 'stripe_payment_received.star' },
      { dir: 'phantom-cost-basis', file: 'corporate_action_cost_adjustment.star' },
      { dir: 'precious-metals', file: 'valuation_on_capture.star' },
      { dir: 'saas-billing', file: 'compute_billing.star' },
      { dir: 'saas-billing', file: 'generate_monthly_invoice.star' },
      { dir: 'saas-billing', file: 'record_gpu_usage.star' },
      { dir: 'time-of-use-pricing', file: 'tou_energy_valuation.star' },
    ]

    for (const { dir, file } of patterns) {
      it(`generates valid Mermaid for ${file}`, () => {
        const source = readPattern(dir, file)
        const flow = parseStarlarkSaga(source)
        const markup = generateMermaidMarkup(flow)

        // Basic structural validation
        expect(markup).toMatch(/^flowchart TD/)
        expect(markup).toContain('START')
        expect(markup).toContain('END')

        // No unclosed brackets or mismatched quotes
        const openBrackets = (markup.match(/\["/g) || []).length
        const closeBrackets = (markup.match(/"\]/g) || []).length
        expect(openBrackets).toBe(closeBrackets)

        // Every step should be connected
        const stepIds = markup.match(/S\d+/g) || []
        for (const id of new Set(stepIds)) {
          // Each step ID should appear at least twice (definition + connection)
          const count = stepIds.filter((s) => s === id).length
          expect(count).toBeGreaterThanOrEqual(2)
        }
      })
    }
  })
})
