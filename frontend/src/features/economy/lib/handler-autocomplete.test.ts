import { describe, it, expect } from 'vitest'
import { EditorState } from '@codemirror/state'
import { CompletionContext } from '@codemirror/autocomplete'
import type { HandlerSchemaResponse } from '@/shared/handler-reference'
import {
  buildHandlerCompletionSource,
  generateParameterTemplate,
  generateHandlerCallTemplate,
} from './handler-autocomplete'

const mockSchema: HandlerSchemaResponse = {
  services: [
    {
      serviceName: 'position_keeping',
      handlers: [
        {
          name: 'initiate_log',
          description: 'Initiates a position log entry',
          params: [
            { name: 'amount', type: 'Decimal', required: true, enumValues: [] },
            { name: 'direction', type: 'enum', required: true, enumValues: ['DEBIT', 'CREDIT'] },
          ],
        },
        {
          name: 'finalize_log',
          description: 'Finalizes a position log entry',
          params: [
            { name: 'log_id', type: 'string', required: true, enumValues: [] },
          ],
        },
        {
          name: 'no_params',
          description: 'Handler with no params',
          params: [],
        },
      ],
    },
    {
      serviceName: 'current_account',
      handlers: [
        {
          name: 'debit',
          description: 'Debits an account',
          params: [
            { name: 'account_id', type: 'string', required: true, enumValues: [] },
            { name: 'amount', type: 'Decimal', required: true, enumValues: [] },
          ],
        },
      ],
    },
  ],
}

/** Helper to create a CompletionContext for the given document text at the end of text */
function makeContext(doc: string, pos?: number): CompletionContext {
  const state = EditorState.create({ doc })
  const cursorPos = pos ?? doc.length
  // explicit=true simulates manual Ctrl+Space trigger
  return new CompletionContext(state, cursorPos, true)
}

describe('generateParameterTemplate', () => {
  it('generates template with multiple params', () => {
    const handler = mockSchema.services[0].handlers[0] // initiate_log
    const result = generateParameterTemplate('position_keeping', handler)
    expect(result).toBe('position_keeping.initiate_log(amount="", direction="DEBIT")')
  })

  it('generates template for enum param using first enum value', () => {
    const handler = mockSchema.services[0].handlers[0] // initiate_log
    const result = generateParameterTemplate('position_keeping', handler)
    expect(result).toContain('direction="DEBIT"')
  })

  it('generates template for handler with no params', () => {
    const handler = mockSchema.services[0].handlers[2] // no_params
    const result = generateParameterTemplate('position_keeping', handler)
    expect(result).toBe('position_keeping.no_params()')
  })

  it('generates template for handler with single param', () => {
    const handler = mockSchema.services[0].handlers[1] // finalize_log
    const result = generateParameterTemplate('position_keeping', handler)
    expect(result).toBe('position_keeping.finalize_log(log_id="")')
  })
})

describe('generateHandlerCallTemplate', () => {
  it('generates handler call without service name prefix', () => {
    const handler = mockSchema.services[0].handlers[0] // initiate_log
    expect(generateHandlerCallTemplate(handler)).toBe('initiate_log(amount="", direction="DEBIT")')
  })

  it('generates bare call for handler with no params', () => {
    const handler = mockSchema.services[0].handlers[2] // no_params
    expect(generateHandlerCallTemplate(handler)).toBe('no_params()')
  })
})

describe('buildHandlerCompletionSource', () => {
  const source = buildHandlerCompletionSource(mockSchema)

  describe('service name completions', () => {
    it('returns service name completions when typing a partial service name', async () => {
      const ctx = makeContext('pos')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const labels = result!.options.map((o) => o.label)
      expect(labels).toContain('position_keeping')
    })

    it('returns matching services when typing a single-character prefix', async () => {
      const ctx = makeContext('p')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const labels = result!.options.map((o) => o.label)
      expect(labels).toContain('position_keeping')
      // 'current_account' does not start with 'p' so should not appear
      expect(labels).not.toContain('current_account')
    })

    it('service completions have namespace type', async () => {
      const ctx = makeContext('pos')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const pos = result!.options.find((o) => o.label === 'position_keeping')
      expect(pos?.type).toBe('namespace')
    })

    it('returns null when no services match', async () => {
      const ctx = makeContext('zzznomatch')
      const result = await source(ctx)
      // When no completions match the word, result is null or has empty options
      if (result !== null) {
        expect(result.options.length).toBe(0)
      }
    })
  })

  describe('handler completions after dot', () => {
    it('returns handler completions after service name and dot', async () => {
      const ctx = makeContext('position_keeping.')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const labels = result!.options.map((o) => o.label)
      expect(labels).toContain('initiate_log')
      expect(labels).toContain('finalize_log')
    })

    it('handler completions have function type', async () => {
      const ctx = makeContext('position_keeping.')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const handler = result!.options.find((o) => o.label === 'initiate_log')
      expect(handler?.type).toBe('function')
    })

    it('handler completions include detail with description', async () => {
      const ctx = makeContext('position_keeping.')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const handler = result!.options.find((o) => o.label === 'initiate_log')
      expect(handler?.detail).toBe('Initiates a position log entry')
    })

    it('filters handlers by partial name after dot', async () => {
      const ctx = makeContext('position_keeping.init')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const labels = result!.options.map((o) => o.label)
      expect(labels).toContain('initiate_log')
      expect(labels).not.toContain('finalize_log')
    })

    it('returns handlers for different service', async () => {
      const ctx = makeContext('current_account.')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const labels = result!.options.map((o) => o.label)
      expect(labels).toContain('debit')
      expect(labels).not.toContain('initiate_log')
    })

    it('returns null for unknown service', async () => {
      const ctx = makeContext('unknown_service.')
      const result = await source(ctx)
      // Either null or empty options
      if (result !== null) {
        expect(result.options.length).toBe(0)
      }
    })

    it('apply inserts handler call without service name prefix', async () => {
      // apply must NOT include the service name — it's already in the document
      const ctx = makeContext('position_keeping.')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const handler = result!.options.find((o) => o.label === 'initiate_log')
      expect(handler?.apply).toBe('initiate_log(amount="", direction="DEBIT")')
      expect(handler?.apply).not.toContain('position_keeping')
    })

    it('apply for handler with no params inserts bare call', async () => {
      const ctx = makeContext('position_keeping.')
      const result = await source(ctx)
      expect(result).not.toBeNull()
      const handler = result!.options.find((o) => o.label === 'no_params')
      expect(handler?.apply).toBe('no_params()')
    })

    it('from is set after the dot so serviceName. in document is preserved', async () => {
      // doc: "position_keeping." — dot is at index 16, so from should be 17
      const doc = 'position_keeping.'
      const ctx = makeContext(doc)
      const result = await source(ctx)
      expect(result).not.toBeNull()
      // from must be right after the dot (dotIndex + 1 = 17)
      expect(result!.from).toBe(doc.indexOf('.') + 1)
    })
  })

  describe('with null schema', () => {
    it('returns null when schema is null', async () => {
      const nullSource = buildHandlerCompletionSource(null)
      const ctx = makeContext('position_keeping.')
      const result = await nullSource(ctx)
      expect(result).toBeNull()
    })
  })
})
