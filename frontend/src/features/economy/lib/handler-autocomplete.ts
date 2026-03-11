import { autocompletion, type CompletionSource, type Completion } from '@codemirror/autocomplete'
import type { Extension } from '@codemirror/state'
import type { HandlerSchemaResponse } from '@/shared/handler-reference'
import { generateHandlerCallTemplate } from '@/shared/handler-template'

export { generateParameterTemplate, generateHandlerCallTemplate } from '@/shared/handler-template'

/**
 * Builds a CodeMirror CompletionSource from a HandlerSchemaResponse.
 *
 * Provides two levels of completion:
 * 1. Service name completions (type: 'namespace') — triggered when typing a word
 * 2. Handler completions (type: 'function') — triggered after "serviceName."
 *    The `apply` string only includes the handler call (no service prefix) since
 *    the service name and dot are already in the document before the cursor.
 *
 * @param schema The handler schema, or null if not yet loaded
 * @returns A CompletionSource for use with autocompletion()
 */
export function buildHandlerCompletionSource(schema: HandlerSchemaResponse | null): CompletionSource {
  return (context) => {
    if (!schema) return null

    // Check for "serviceName." pattern — handler completion
    const dotMatch = context.matchBefore(/[\w_]+\.[\w_]*/)
    if (dotMatch) {
      const text = dotMatch.text
      const dotIndex = text.indexOf('.')
      const serviceName = text.slice(0, dotIndex)
      const handlerPrefix = text.slice(dotIndex + 1)

      const service = schema.services.find((s) => s.serviceName === serviceName)
      if (!service) return { from: dotMatch.from + dotIndex + 1, options: [] }

      const options: Completion[] = service.handlers
        .filter((h) => h.name.startsWith(handlerPrefix))
        .map((h) => ({
          label: h.name,
          type: 'function',
          detail: h.description || undefined,
          // apply only inserts the handler call — the service name and dot are
          // already in the document, so from is set to after the dot
          apply: generateHandlerCallTemplate(h),
        }))

      return { from: dotMatch.from + dotIndex + 1, options }
    }

    // Service name completion — triggered when typing a word (no dot yet)
    const wordMatch = context.matchBefore(/[\w_]+/)
    if (!wordMatch) return null

    const prefix = wordMatch.text
    const options: Completion[] = schema.services
      .filter((s) => s.serviceName.startsWith(prefix))
      .map((s) => ({
        label: s.serviceName,
        type: 'namespace',
      }))

    if (options.length === 0) return null
    return { from: wordMatch.from, options }
  }
}

/**
 * Creates a CodeMirror extension that provides handler autocomplete.
 *
 * @param schema The handler schema to source completions from (null disables completions)
 * @returns A CodeMirror Extension
 */
export function handlerAutocomplete(schema: HandlerSchemaResponse | null): Extension {
  return autocompletion({
    override: [buildHandlerCompletionSource(schema)],
  })
}
