import { useEffect, useState, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTenantSlug } from './use-tenant-context'

/**
 * Represents a domain event from the backend event stream.
 * This stub supports the Phase 4 WebSocket integration.
 *
 * @example
 * {
 *   eventType: 'AccountStatusChanged',
 *   tenantSlug: 'acme',
 *   payload: { accountId: '123', status: 'ACTIVE' },
 *   timestamp: new Date()
 * }
 */
export interface DomainEvent {
  eventType: string
  tenantSlug: string
  payload: Record<string, unknown>
  timestamp: Date
}

/**
 * Configuration options for useEventStream hook.
 * This interface defines how the hook should behave when events are received.
 */
export interface UseEventStreamOptions {
  /** Filter events by type. If undefined, all event types are accepted. */
  eventTypes?: string[]
  /** Callback fired when an event matching filters is received. */
  onEvent?: (event: DomainEvent) => void
  /** Whether to automatically invalidate React Query caches based on event type. Defaults to true. */
  autoInvalidate?: boolean
}

/**
 * Maps domain event types to React Query cache keys that should be invalidated.
 * This enables automatic cache invalidation when events are received.
 *
 * Example: When 'AccountStatusChanged' is received, invalidate accounts and account-specific queries.
 */
export const EVENT_QUERY_MAP: Record<string, (event: DomainEvent) => unknown[][]> = {
  AccountStatusChanged: (e) => [
    ['accounts', e.tenantSlug],
    ['accounts', e.tenantSlug, { accountId: e.payload.accountId }],
  ],
  TransactionCompleted: (e) => [
    ['accounts', e.tenantSlug, { accountId: e.payload.accountId }],
    ['position-logs', e.tenantSlug],
  ],
  PaymentOrderCompleted: (e) => [
    ['payment-orders', e.tenantSlug],
    ['payment-orders', e.tenantSlug, { paymentOrderId: e.payload.paymentOrderId }],
  ],
}

/**
 * React hook for consuming real-time domain events.
 * This is a stub implementation that provides the complete interface for Phase 3 component development.
 * The actual WebSocket connection will be implemented in Phase 4 (PRD-025).
 *
 * @param options Configuration options for event filtering, callbacks, and cache invalidation
 * @returns Object with connection status, last received event, and any connection errors
 *
 * @example
 * const { connected, lastEvent, error } = useEventStream({
 *   eventTypes: ['AccountStatusChanged', 'TransactionCompleted'],
 *   onEvent: (event) => console.log('Event received:', event),
 *   autoInvalidate: true
 * })
 *
 * @phase 3 (Stub for Phase 4 WebSocket integration)
 */
export function useEventStream(options: UseEventStreamOptions = {}) {
  const { autoInvalidate = true, onEvent, eventTypes } = options
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  const [connected, setConnected] = useState(false)
  const [lastEvent, setLastEvent] = useState<DomainEvent | null>(null)
  const [error, setError] = useState<Error | null>(null)

  // Phase 4: WebSocket connection setup
  // This useEffect will be implemented in Phase 4 (PRD-025) to connect to:
  // const ws = new WebSocket(`wss://${tenantSlug}.api.meridian.io/events`)
  // The connection will:
  // - Set connected = true on open
  // - Call handleEvent on message with decoded event data
  // - Set error and connected = false on close/error
  // - Clean up WebSocket on unmount
  useEffect(() => {
    // STUB: Phase 4 implementation
    // For now, this hook provides the interface without connection logic
    setConnected(false)

    return () => {
      // Phase 4: Cleanup WebSocket connection
    }
  }, [tenantSlug])

  /**
   * Internal handler for processing received events.
   * Applies event type filtering, fires the onEvent callback, and triggers cache invalidation.
   */
  const handleEvent = useCallback(
    (event: DomainEvent) => {
      // Filter by event types if specified
      if (eventTypes && !eventTypes.includes(event.eventType)) {
        return
      }

      // Update last event state
      setLastEvent(event)

      // Fire user callback if provided
      onEvent?.(event)

      // Auto-invalidate queries based on event type
      if (autoInvalidate) {
        const keys = EVENT_QUERY_MAP[event.eventType]?.(event) ?? []
        keys.forEach((key) => {
          queryClient.invalidateQueries({ queryKey: key })
        })
      }
    },
    [eventTypes, onEvent, autoInvalidate, queryClient]
  )

  return {
    /** Whether the WebSocket is currently connected (Phase 4) */
    connected,
    /** The last event received, or null if no events yet */
    lastEvent,
    /** Any connection error that occurred, or null if connected successfully */
    error,
  }
}
