import { ConnectError, Code } from '@connectrpc/connect'

/**
 * Checks if an error is a version conflict (optimistic locking failure).
 * The server returns gRPC ABORTED when expectedSequenceNumber doesn't match.
 */
export function isVersionConflict(err: unknown): boolean {
  return err instanceof ConnectError && err.code === Code.Aborted
}
