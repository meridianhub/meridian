import { describe, it, expect } from 'vitest'
import { generateParameterTemplate, generateHandlerCallTemplate, buildParamString } from './handler-template'
import type { Handler } from './handler-reference'

const handlerWithParams: Handler = {
  name: 'initiate_log',
  description: 'Initiates a log',
  params: [
    { name: 'amount', type: 'Decimal', required: true, enumValues: [] },
    { name: 'direction', type: 'enum', required: true, enumValues: ['DEBIT', 'CREDIT'] },
  ],
}

const handlerNoParams: Handler = {
  name: 'finalize',
  description: '',
  params: [],
}

describe('buildParamString', () => {
  it('builds comma-separated param assignments', () => {
    expect(buildParamString(handlerWithParams)).toBe('amount="", direction="DEBIT"')
  })

  it('returns empty string for handler with no params', () => {
    expect(buildParamString(handlerNoParams)).toBe('')
  })

  it('uses first enum value for enum params', () => {
    expect(buildParamString(handlerWithParams)).toContain('direction="DEBIT"')
  })
})

describe('generateParameterTemplate', () => {
  it('generates full template with service name prefix', () => {
    expect(generateParameterTemplate('position_keeping', handlerWithParams)).toBe(
      'position_keeping.initiate_log(amount="", direction="DEBIT")',
    )
  })

  it('generates template for handler with no params', () => {
    expect(generateParameterTemplate('svc', handlerNoParams)).toBe('svc.finalize()')
  })
})

describe('generateHandlerCallTemplate', () => {
  it('generates call without service name prefix', () => {
    expect(generateHandlerCallTemplate(handlerWithParams)).toBe('initiate_log(amount="", direction="DEBIT")')
  })

  it('generates bare call for handler with no params', () => {
    expect(generateHandlerCallTemplate(handlerNoParams)).toBe('finalize()')
  })
})
