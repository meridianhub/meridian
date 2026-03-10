import { QueryClient } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'

/**
 * gRPC/Connect status codes that correspond to client errors (4xx equivalent).
 * These will not succeed on retry, so we skip them immediately.
 */
const NON_RETRYABLE_CODES: ReadonlySet<Code> = new Set([
  Code.InvalidArgument,
  Code.NotFound,
  Code.AlreadyExists,
  Code.PermissionDenied,
  Code.Unauthenticated,
  Code.FailedPrecondition,
  Code.OutOfRange,
  Code.Unimplemented,
])

/**
 * Determines whether a failed query should be retried.
 * - Client errors (4xx-equivalent gRPC codes) are never retried.
 * - Server errors (5xx) and network failures retry up to 3 attempts.
 */
export function shouldRetry(failureCount: number, error: Error): boolean {
  if (error instanceof ConnectError && NON_RETRYABLE_CODES.has(error.code)) {
    return false
  }
  return failureCount < 3
}

/**
 * Exponential backoff: 1s, 2s, 4s... capped at 10s.
 */
export function retryDelay(attempt: number): number {
  return Math.min(1000 * 2 ** attempt, 10_000)
}

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 5 * 60 * 1000,
      retry: shouldRetry,
      retryDelay,
      refetchOnWindowFocus: true,
      refetchOnReconnect: true,
    },
    mutations: { retry: 0 },
  },
})
