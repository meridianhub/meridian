import type { Handler } from './handler-reference'

/**
 * Builds the parameter string for a handler call.
 * Example: amount="", direction="DEBIT"
 */
export function buildParamString(handler: Handler): string {
  return handler.params
    .map((p) => {
      const value = p.type === 'enum' ? `"${p.enumValues[0] ?? ''}"` : '""'
      return `${p.name}=${value}`
    })
    .join(', ')
}

/**
 * Generates a full Starlark call template including the service name.
 * Example: position_keeping.initiate_log(amount="", direction="DEBIT")
 */
export function generateParameterTemplate(serviceName: string, handler: Handler): string {
  return `${serviceName}.${handler.name}(${buildParamString(handler)})`
}

/**
 * Generates only the handler call portion (no service name prefix).
 * Used where the service name and dot are already present in the target document.
 * Example: initiate_log(amount="", direction="DEBIT")
 */
export function generateHandlerCallTemplate(handler: Handler): string {
  return `${handler.name}(${buildParamString(handler)})`
}
