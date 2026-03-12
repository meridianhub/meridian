import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { join } from 'path'
import { parseStarlarkSaga, analyzeSagaOutputs } from './star-parser'

const PATTERNS_DIR = join(__dirname, '..', '..', '..', '..', 'cookbook', 'patterns')

function readPattern(subdir: string, file: string): string {
  return readFileSync(join(PATTERNS_DIR, subdir, file), 'utf-8')
}

describe('parseStarlarkSaga', () => {
  describe('saga name extraction', () => {
    it('extracts name from saga() call', () => {
      const result = parseStarlarkSaga('x = saga(name="my_saga")\nstep(name="s1")\n')
      expect(result.name).toBe('my_saga')
    })

    it('falls back to header comment', () => {
      const result = parseStarlarkSaga('# Saga: fallback_name\ndef run():\n  step(name="s1")\n')
      expect(result.name).toBe('fallback_name')
    })

    it('returns unknown for no name', () => {
      const result = parseStarlarkSaga('step(name="s1")\n')
      expect(result.name).toBe('unknown')
    })
  })

  describe('trigger extraction', () => {
    it('extracts event trigger', () => {
      const result = parseStarlarkSaga(
        '# Trigger: event:position-keeping.transaction-captured.v1\nsaga(name="t")\n',
      )
      expect(result.trigger).toBe('event:position-keeping.transaction-captured.v1')
    })

    it('extracts scheduled trigger', () => {
      const result = parseStarlarkSaga(
        '# Trigger: scheduled:monthly_billing\nsaga(name="t")\n',
      )
      expect(result.trigger).toBe('scheduled:monthly_billing')
    })

    it('extracts webhook trigger', () => {
      const result = parseStarlarkSaga(
        '# Trigger: webhook:gpu_meter_event\nsaga(name="t")\n',
      )
      expect(result.trigger).toBe('webhook:gpu_meter_event')
    })

    it('returns null when no trigger', () => {
      const result = parseStarlarkSaga('saga(name="t")\n')
      expect(result.trigger).toBeNull()
    })
  })

  describe('filter extraction', () => {
    it('extracts CEL filter', () => {
      const result = parseStarlarkSaga(
        "# Filter:  event.instrument_code != 'GBP' && event.direction == 'DEBIT'\nsaga(name=\"t\")\n",
      )
      expect(result.filter).toBe("event.instrument_code != 'GBP' && event.direction == 'DEBIT'")
    })

    it('returns null when no filter', () => {
      const result = parseStarlarkSaga('saga(name="t")\n')
      expect(result.filter).toBeNull()
    })
  })

  describe('step extraction', () => {
    it('extracts basic steps', () => {
      const source = `
saga(name="test")
def run():
    step(name="step_one")
    result = reference_data.get_account(id=account_id)
    step(name="step_two")
    position_keeping.initiate_log(account_id=aid, amount=amt)
`
      const result = parseStarlarkSaga(source)
      expect(result.steps).toHaveLength(2)
      expect(result.steps[0].name).toBe('step_one')
      expect(result.steps[1].name).toBe('step_two')
    })

    it('extracts service calls within step blocks', () => {
      const source = `
saga(name="test")
step(name="lookup")
account = reference_data.get_account(id=source_id)
step(name="book")
position_keeping.initiate_log(account_id=aid, amount=amt, direction="DEBIT")
`
      const result = parseStarlarkSaga(source)
      expect(result.steps[0].serviceCalls).toHaveLength(1)
      expect(result.steps[0].serviceCalls[0]).toEqual({
        service: 'reference_data',
        method: 'get_account',
        params: ['id'],
      })
      expect(result.steps[1].serviceCalls).toHaveLength(1)
      expect(result.steps[1].serviceCalls[0]).toEqual({
        service: 'position_keeping',
        method: 'initiate_log',
        params: ['account_id', 'amount', 'direction'],
      })
    })

    it('extracts early exit with return status', () => {
      const source = `
saga(name="test")
step(name="check")
existing = position_keeping.query_logs(correlation_id=cid)
if existing.count > 0:
    return {"status": "ALREADY_PROCESSED", "id": cid}
step(name="book")
position_keeping.initiate_log(account_id=aid)
`
      const result = parseStarlarkSaga(source)
      expect(result.steps[0].earlyExit).toEqual({
        condition: 'existing.count > 0',
        returnStatus: 'ALREADY_PROCESSED',
      })
      expect(result.steps[1].earlyExit).toBeNull()
    })
  })

  describe('edge cases', () => {
    it('handles empty source', () => {
      const result = parseStarlarkSaga('')
      expect(result.name).toBe('unknown')
      expect(result.trigger).toBeNull()
      expect(result.filter).toBeNull()
      expect(result.steps).toHaveLength(0)
    })

    it('handles saga with no steps', () => {
      const result = parseStarlarkSaga('saga(name="empty")\ndef run():\n    return {"status": "OK"}\n')
      expect(result.name).toBe('empty')
      expect(result.steps).toHaveLength(0)
    })

    it('handles step with no service calls', () => {
      const source = `
saga(name="test")
step(name="validate")
if not billing_account_id:
    return {"status": "CONFIG_ERROR"}
step(name="book")
position_keeping.initiate_log(account_id=aid)
`
      const result = parseStarlarkSaga(source)
      expect(result.steps[0].serviceCalls).toHaveLength(0)
    })
  })

  // Real .star file tests
  describe('real patterns', () => {
    it('parses usage_to_value.star', () => {
      const source = readPattern('energy-settlement', 'usage_to_value.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('usage_to_value')
      expect(result.trigger).toBe('event:position-keeping.transaction-captured.v1')
      expect(result.filter).toContain("event.instrument_code != 'GBP'")
      expect(result.steps.length).toBeGreaterThanOrEqual(7)

      // Check known steps exist
      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('lookup_account')
      expect(stepNames).toContain('check_retail_idempotency')
      expect(stepNames).toContain('compute_retail_valuation')
      expect(stepNames).toContain('book_retail_position')
      expect(stepNames).toContain('book_wholesale_position')

      // Verify service calls
      const lookupStep = result.steps.find((s) => s.name === 'lookup_account')!
      expect(lookupStep.serviceCalls[0].service).toBe('reference_data')
      expect(lookupStep.serviceCalls[0].method).toBe('get_account')
    })

    it('parses dynamic_capacity_billing.star', () => {
      const source = readPattern('dynamic-capacity-pricing', 'dynamic_capacity_billing.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('dynamic_capacity_billing')
      expect(result.trigger).toBe('event:position-keeping.transaction-captured.v1')
      expect(result.filter).toContain("event.instrument_code == 'TOKEN'")

      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('lookup_account')
      expect(stepNames).toContain('check_idempotency')
      expect(stepNames).toContain('lookup_regional_price')
      expect(stepNames).toContain('book_charge')

      // Check market_data service call
      const priceStep = result.steps.find((s) => s.name === 'lookup_regional_price')!
      expect(priceStep.serviceCalls[0].service).toBe('market_data')
      expect(priceStep.serviceCalls[0].method).toBe('get_observation')
    })

    it('parses race_result_distribution.star (for loop with dynamic steps)', () => {
      const source = readPattern('entity-distribution', 'race_result_distribution.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('race_result_distribution')
      expect(result.trigger).toBe('event:market-information.observation-recorded.v1')

      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('check_idempotency')
      expect(stepNames).toContain('find_syndicate')
      expect(stepNames).toContain('list_participants')
      // Dynamic steps should have wildcard suffix
      const hasDynamicStructuring = stepNames.some((n) => n.startsWith('get_structuring'))
      const hasDynamicPayout = stepNames.some((n) => n.startsWith('book_payout'))
      expect(hasDynamicStructuring).toBe(true)
      expect(hasDynamicPayout).toBe(true)
    })

    it('parses kyc_on_party.star', () => {
      const source = readPattern('kyc-compliance', 'kyc_on_party.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('kyc_on_party')
      expect(result.trigger).toBe('event:party.created.v1')
      expect(result.filter).toContain("event.party_type == 'INDIVIDUAL'")

      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('lookup_party')
      expect(stepNames).toContain('check_idempotency')
      expect(stepNames).toContain('find_compliance_account')
      expect(stepNames).toContain('book_kyc_marker')
    })

    it('parses stripe_payment_received.star', () => {
      const source = readPattern('payment-gateway-stripe', 'stripe_payment_received.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('stripe_payment_received')
      // No trigger/filter headers in this file
      expect(result.steps.length).toBe(2)
      expect(result.steps[0].name).toBe('debit_clearing')
      expect(result.steps[1].name).toBe('credit_customer')

      // Both steps call position_keeping.initiate_log
      expect(result.steps[0].serviceCalls[0].service).toBe('position_keeping')
      expect(result.steps[1].serviceCalls[0].service).toBe('position_keeping')
    })

    it('parses corporate_action_cost_adjustment.star (for loop)', () => {
      const source = readPattern('phantom-cost-basis', 'corporate_action_cost_adjustment.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('corporate_action_cost_adjustment')
      expect(result.trigger).toBe('event:market-information.observation-recorded.v1')

      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('check_idempotency')
      expect(stepNames).toContain('find_holdings')
    })

    it('parses valuation_on_capture.star', () => {
      const source = readPattern('precious-metals', 'valuation_on_capture.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('valuation_on_capture')
      expect(result.trigger).toBe('event:position-keeping.transaction-captured.v1')

      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('lookup_account')
      expect(stepNames).toContain('check_idempotency')
      expect(stepNames).toContain('compute_valuation')
      expect(stepNames).toContain('book_settlement')
    })

    it('parses compute_billing.star', () => {
      const source = readPattern('saas-billing', 'compute_billing.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('compute_billing')
      expect(result.steps.length).toBe(5)

      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('lookup_account')
      expect(stepNames).toContain('check_idempotency')
      expect(stepNames).toContain('lookup_account_type')
      expect(stepNames).toContain('compute_charge')
      expect(stepNames).toContain('book_charge')
    })

    it('parses generate_monthly_invoice.star (nested for loops)', () => {
      const source = readPattern('saas-billing', 'generate_monthly_invoice.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('generate_monthly_invoice')
      expect(result.trigger).toBe('scheduled:monthly_billing')

      // Should have the list step plus dynamic steps
      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('list_usage_accounts')
    })

    it('parses record_gpu_usage.star', () => {
      const source = readPattern('saas-billing', 'record_gpu_usage.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('record_gpu_usage')
      expect(result.trigger).toBe('webhook:gpu_meter_event')
      expect(result.steps.length).toBe(1)
      expect(result.steps[0].name).toBe('record_usage')
      expect(result.steps[0].serviceCalls[0].service).toBe('position_keeping')
    })

    it('parses tou_energy_valuation.star', () => {
      const source = readPattern('time-of-use-pricing', 'tou_energy_valuation.star')
      const result = parseStarlarkSaga(source)

      expect(result.name).toBe('tou_energy_valuation')
      expect(result.trigger).toBe('event:position-keeping.transaction-captured.v1')
      expect(result.filter).toContain("event.instrument_code == 'KWH'")

      const stepNames = result.steps.map((s) => s.name)
      expect(stepNames).toContain('lookup_account')
      expect(stepNames).toContain('check_idempotency')
      expect(stepNames).toContain('lookup_account_type')
      expect(stepNames).toContain('compute_tou_valuation')
      expect(stepNames).toContain('book_charge')
    })
  })

  describe('multiple early exits', () => {
    it('captures first early exit per step block', () => {
      const source = readPattern('energy-settlement', 'usage_to_value.star')
      const result = parseStarlarkSaga(source)

      // The lookup_account step has an early exit for CONFIG_ERROR
      const lookupStep = result.steps.find((s) => s.name === 'lookup_account')!
      expect(lookupStep.earlyExit).not.toBeNull()
      expect(lookupStep.earlyExit!.returnStatus).toBe('CONFIG_ERROR')
    })
  })
})

describe('analyzeSagaOutputs', () => {
  describe('literal value extraction', () => {
    it('extracts literal instrument_code, account_id, and direction', () => {
      const source = `
saga(name="test")
step(name="book")
position_keeping.initiate_log(
    account_id="ACC-001",
    instrument_code="GBP",
    direction="DEBIT",
    amount=amt,
)
`
      const result = analyzeSagaOutputs(source)
      expect(result.producedEvents).toHaveLength(1)
      expect(result.producedEvents[0]).toEqual({
        stepName: 'book',
        lineNumber: expect.any(Number),
        instrumentCode: 'GBP',
        accountId: 'ACC-001',
        direction: 'DEBIT',
      })
      expect(result.dynamicTargets).toHaveLength(0)
    })
  })

  describe('dynamic variable detection', () => {
    it('detects dynamic instrument_code and account_id', () => {
      const source = `
saga(name="test")
step(name="book")
position_keeping.initiate_log(account_id=billing_account_id, instrument_code=inst_code, direction="CREDIT", amount=amt)
`
      const result = analyzeSagaOutputs(source)
      expect(result.producedEvents).toHaveLength(1)
      expect(result.producedEvents[0].instrumentCode).toBeNull()
      expect(result.producedEvents[0].accountId).toBeNull()
      expect(result.producedEvents[0].direction).toBe('CREDIT')
      expect(result.dynamicTargets).toHaveLength(2)
      expect(result.dynamicTargets[0].variableName).toBe('inst_code')
      expect(result.dynamicTargets[1].variableName).toBe('billing_account_id')
    })
  })

  describe('valuation_engine.compute extraction', () => {
    it('extracts valuation call parameters', () => {
      const source = `
saga(name="test")
step(name="compute")
retail = valuation_engine.compute(
    method_id="SPOT_RATE",
    amount=amount,
    from_instrument="KWH",
    to_instrument="GBP",
)
`
      const result = analyzeSagaOutputs(source)
      expect(result.valuationCalls).toHaveLength(1)
      expect(result.valuationCalls[0]).toEqual({
        stepName: 'compute',
        lineNumber: expect.any(Number),
        fromInstrument: 'KWH',
        toInstrument: 'GBP',
        methodId: 'SPOT_RATE',
      })
    })

    it('detects dynamic method_id and from_instrument', () => {
      const source = `
saga(name="test")
step(name="compute")
result = valuation_engine.compute(method_id=method, amount=amt, from_instrument=src_instr, to_instrument="GBP")
`
      const result = analyzeSagaOutputs(source)
      expect(result.valuationCalls).toHaveLength(1)
      expect(result.valuationCalls[0].methodId).toBeNull()
      expect(result.valuationCalls[0].fromInstrument).toBeNull()
      expect(result.valuationCalls[0].toInstrument).toBe('GBP')
      expect(result.dynamicTargets).toHaveLength(2)
      const varNames = result.dynamicTargets.map((d) => d.variableName)
      expect(varNames).toContain('src_instr')
      expect(varNames).toContain('method')
    })
  })

  describe('multi-step saga', () => {
    it('extracts multiple initiate_log calls across steps', () => {
      const source = `
saga(name="test")
step(name="book_retail")
position_keeping.initiate_log(account_id=billing_id, instrument_code="GBP", direction="DEBIT", amount=retail_amt)
step(name="book_wholesale")
position_keeping.initiate_log(account_id=counterparty_id, instrument_code="GBP", direction="CREDIT", amount=wholesale_amt)
`
      const result = analyzeSagaOutputs(source)
      expect(result.producedEvents).toHaveLength(2)
      expect(result.producedEvents[0].stepName).toBe('book_retail')
      expect(result.producedEvents[0].instrumentCode).toBe('GBP')
      expect(result.producedEvents[0].direction).toBe('DEBIT')
      expect(result.producedEvents[1].stepName).toBe('book_wholesale')
      expect(result.producedEvents[1].instrumentCode).toBe('GBP')
      expect(result.producedEvents[1].direction).toBe('CREDIT')
    })
  })

  describe('empty saga', () => {
    it('returns empty arrays for saga with no position_keeping calls', () => {
      const source = `
saga(name="empty")
step(name="lookup")
account = reference_data.get_account(id=aid)
`
      const result = analyzeSagaOutputs(source)
      expect(result.producedEvents).toHaveLength(0)
      expect(result.valuationCalls).toHaveLength(0)
      expect(result.dynamicTargets).toHaveLength(0)
    })
  })

  describe('real patterns', () => {
    it('analyzes usage_to_value.star outputs', () => {
      const source = readPattern('energy-settlement', 'usage_to_value.star')
      const result = analyzeSagaOutputs(source)

      // Two position_keeping.initiate_log calls (retail + wholesale)
      expect(result.producedEvents).toHaveLength(2)
      expect(result.producedEvents[0].stepName).toBe('book_retail_position')
      expect(result.producedEvents[0].instrumentCode).toBe('GBP')
      expect(result.producedEvents[0].direction).toBe('DEBIT')
      expect(result.producedEvents[1].stepName).toBe('book_wholesale_position')
      expect(result.producedEvents[1].instrumentCode).toBe('GBP')
      expect(result.producedEvents[1].direction).toBe('CREDIT')

      // Two valuation_engine.compute calls
      expect(result.valuationCalls).toHaveLength(2)
      expect(result.valuationCalls[0].stepName).toBe('compute_retail_valuation')
      expect(result.valuationCalls[0].toInstrument).toBe('GBP')
      expect(result.valuationCalls[0].fromInstrument).toBeNull() // dynamic: instrument_code variable
      expect(result.valuationCalls[1].stepName).toBe('compute_wholesale_valuation')

      // Dynamic targets for from_instrument (variable references)
      expect(result.dynamicTargets.length).toBeGreaterThan(0)
      const dynamicVars = result.dynamicTargets.map((d) => d.variableName)
      expect(dynamicVars).toContain('instrument_code')
    })

    it('analyzes stripe_payment_received.star outputs', () => {
      const source = readPattern('payment-gateway-stripe', 'stripe_payment_received.star')
      const result = analyzeSagaOutputs(source)

      // Two position_keeping.initiate_log calls
      expect(result.producedEvents).toHaveLength(2)
      expect(result.producedEvents[0].stepName).toBe('debit_clearing')
      expect(result.producedEvents[1].stepName).toBe('credit_customer')

      // No valuation calls
      expect(result.valuationCalls).toHaveLength(0)
    })
  })
})
