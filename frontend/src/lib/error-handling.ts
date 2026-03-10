import { Code, ConnectError } from '@connectrpc/connect'
import { useCallback } from 'react'
import { toast } from 'sonner'

/**
 * Structured result from handling a ConnectError.
 */
export interface ConnectErrorResult {
  /** User-friendly error message */
  message: string
  /** The gRPC/Connect status code */
  code: Code
  /** Field-level validation errors extracted from google.rpc.BadRequest details */
  fieldErrors: Record<string, string>
  /** Whether the caller should retry the operation */
  shouldRetry: boolean
  /** Path to redirect to, if applicable (e.g. /login for Unauthenticated) */
  redirectTo?: string
  /** The original error */
  originalError: unknown
}

const ERROR_MESSAGES: Record<number, string> = {
  [Code.Canceled]: 'The operation was canceled.',
  [Code.Unknown]: 'An unexpected error occurred. Please try again.',
  [Code.InvalidArgument]: 'The request contains invalid data.',
  [Code.DeadlineExceeded]: 'The request timed out. Please try again.',
  [Code.NotFound]: 'The requested resource was not found.',
  [Code.AlreadyExists]: 'This resource already exists.',
  [Code.PermissionDenied]: 'You do not have permission to perform this action.',
  [Code.ResourceExhausted]: 'Rate limit exceeded. Please slow down and try again.',
  [Code.FailedPrecondition]: 'The operation cannot be performed in the current state.',
  [Code.Aborted]: 'The operation was aborted. Please try again.',
  [Code.OutOfRange]: 'The provided value is out of the allowed range.',
  [Code.Unimplemented]: 'This feature is not yet available.',
  [Code.Internal]: 'An internal server error occurred. Please contact support if the problem persists.',
  [Code.Unavailable]: 'The service is temporarily unavailable. Please try again shortly.',
  [Code.DataLoss]: 'A data integrity error occurred. Please contact support.',
  [Code.Unauthenticated]: 'Your session has expired. Please sign in again.',
}

type IncomingDetail = {
  type: string
  value: Uint8Array
  debug?: unknown
}

interface BadRequestFieldViolation {
  field: string
  description: string
}

interface BadRequestDebug {
  fieldViolations?: BadRequestFieldViolation[]
}

/**
 * Extracts field-level errors from google.rpc.BadRequest error details.
 */
function extractFieldErrors(details: (IncomingDetail | object)[]): Record<string, string> {
  const fieldErrors: Record<string, string> = {}

  for (const detail of details) {
    const d = detail as IncomingDetail
    if (d.type !== 'google.rpc.BadRequest') continue

    const debugData = d.debug as BadRequestDebug | null | undefined
    const violations = debugData?.fieldViolations
    if (!Array.isArray(violations)) continue

    for (const violation of violations) {
      if (
        violation &&
        typeof violation === 'object' &&
        typeof (violation as BadRequestFieldViolation).field === 'string' &&
        typeof (violation as BadRequestFieldViolation).description === 'string'
      ) {
        fieldErrors[(violation as BadRequestFieldViolation).field] =
          (violation as BadRequestFieldViolation).description
      }
    }
  }

  return fieldErrors
}

export interface HandleConnectErrorOptions {
  /** When provided, sets redirectTo to this path for NotFound errors */
  navigateToParent?: string
  /** When true, shows a toast notification for the error */
  showToast?: boolean
}

/**
 * Maps a ConnectError (or any thrown value) to a structured ConnectErrorResult.
 *
 * - Unauthenticated → redirectTo: '/login'
 * - NotFound + navigateToParent option → redirectTo: navigateToParent
 * - InvalidArgument → extracts field errors from google.rpc.BadRequest details
 * - Unavailable → shouldRetry: true
 */
export function handleConnectError(
  error: unknown,
  options: HandleConnectErrorOptions = {},
): ConnectErrorResult {
  const connectError = ConnectError.from(error)
  const code = connectError.code
  const message =
    ERROR_MESSAGES[code] ?? 'An unexpected error occurred. Please try again.'

  const fieldErrors =
    code === Code.InvalidArgument
      ? extractFieldErrors(connectError.details)
      : {}

  let redirectTo: string | undefined
  if (code === Code.Unauthenticated) {
    redirectTo = '/login'
  } else if (code === Code.NotFound && options.navigateToParent) {
    redirectTo = options.navigateToParent
  }

  const shouldRetry = code === Code.Unavailable

  if (options.showToast) {
    toast.error(message)
  }

  return {
    message,
    code,
    fieldErrors,
    shouldRetry,
    redirectTo,
    originalError: error,
  }
}

export interface WithErrorHandlingOptions extends HandleConnectErrorOptions {
  /** Called with the structured error result */
  onError: (result: ConnectErrorResult) => void
  /** Called with the redirect path when a redirect is required */
  onRedirect?: (path: string) => void
}

/**
 * Creates a React Query mutation onError handler that maps gRPC errors
 * to structured ConnectErrorResult values.
 *
 * Usage:
 *   useMutation({
 *     ...withErrorHandling({
 *       onError: (result) => setError(result.message),
 *       onRedirect: (path) => navigate(path),
 *     })
 *   })
 */
export function withErrorHandling(options: WithErrorHandlingOptions) {
  const { onError, onRedirect, ...handlerOptions } = options

  return {
    onError(error: unknown) {
      const result = handleConnectError(error, handlerOptions)
      onError(result)
      if (result.redirectTo && onRedirect) {
        onRedirect(result.redirectTo)
      }
    },
  }
}

/**
 * Convenience wrapper for mutations that should show toast errors.
 * Shows a toast notification and optionally calls onError/onRedirect.
 *
 * Usage:
 *   useMutation({
 *     ...withToastErrorHandling()
 *   })
 *
 *   useMutation({
 *     ...withToastErrorHandling({
 *       onError: (result) => setFieldErrors(result.fieldErrors),
 *       onRedirect: (path) => navigate(path),
 *     })
 *   })
 */
export function withToastErrorHandling(
  options: Partial<WithErrorHandlingOptions> = {},
) {
  return withErrorHandling({
    ...options,
    showToast: true,
    onError: options.onError ?? (() => {}),
  })
}

/**
 * React hook that returns a stable handleConnectError function bound to
 * the provided options.
 */
export function useErrorHandler(options: HandleConnectErrorOptions = {}) {
  return useCallback(
    (error: unknown) => handleConnectError(error, options),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [options.navigateToParent],
  )
}
