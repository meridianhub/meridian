import { useEffect, useState, useCallback, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTenantSlug } from './use-tenant-context'

/**
 * Represents a domain event from the backend event stream.
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
 * Query keys follow the tenantKeys factory structure: ['tenants', tenantId, 'resource', ...]
 * Example: When 'AccountStatusChanged' is received, invalidate accounts and account-specific queries.
 */
export const EVENT_QUERY_MAP: Record<string, (event: DomainEvent) => unknown[][]> = {
  AccountStatusChanged: (e) => [
    ['tenants', e.tenantSlug, 'accounts'],
    ['tenants', e.tenantSlug, 'accounts', e.payload.accountId],
  ],
  TransactionCompleted: (e) => [
    ['tenants', e.tenantSlug, 'accounts', e.payload.accountId],
    ['tenants', e.tenantSlug, 'position-logs'],
  ],
  PaymentOrderCompleted: (e) => [
    ['tenants', e.tenantSlug, 'payment-orders'],
    ['tenants', e.tenantSlug, 'payment-orders', e.payload.paymentOrderId],
  ],
}

/** Wire format of a message received from the gateway WebSocket endpoint. */
interface ServerMessage {
  type: 'event' | 'subscribed' | 'error' | 'system'
  subscription_id?: string
  channel?: string
  event?: {
    event_id: string
    event_type: string
    aggregate_id?: string
    aggregate_type?: string
    tenant_id: string
    correlation_id?: string
    causation_id?: string
    timestamp: string
    payload: Record<string, unknown>
  }
  error_code?: string
  error_message?: string
  system_message?: string
}

const MAX_RECONNECT_ATTEMPTS = 5
const BASE_RECONNECT_DELAY_MS = 1000

/**
 * React hook for consuming real-time domain events via WebSocket.
 *
 * Connects to the gateway's /ws/events endpoint and delivers events to
 * the caller through the onEvent callback and React Query cache invalidation.
 *
 * Enforces tenant isolation - events from other tenants are silently ignored.
 *
 * @param options Configuration options for event filtering, callbacks, and cache invalidation
 * @returns Object with connection status and last received event
 *
 * @example
 * const { connected, lastEvent } = useEventStream({
 *   eventTypes: ['AccountStatusChanged', 'TransactionCompleted'],
 *   onEvent: (event) => console.log('Event received:', event),
 *   autoInvalidate: true
 * })
 */
export function useEventStream(options: UseEventStreamOptions = {}) {
  const { autoInvalidate = true, onEvent, eventTypes } = options
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  const [connected, setConnected] = useState(false)
  const [lastEvent, setLastEvent] = useState<DomainEvent | null>(null)

  /**
   * Internal handler for processing received events.
   * Validates tenant isolation, applies event type filtering, fires callbacks, and triggers cache invalidation.
   */
  const handleEvent = useCallback(
    (event: DomainEvent) => {
      // Enforce tenant isolation - ignore events from other tenants
      if (event.tenantSlug !== tenantSlug) {
        return
      }

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
    [eventTypes, onEvent, autoInvalidate, queryClient, tenantSlug]
  )

  // Stable ref for handleEvent so the WebSocket effect does not re-run when
  // the callback identity changes.
  const handleEventRef = useRef(handleEvent)
  handleEventRef.current = handleEvent

  useEffect(() => {
    if (!tenantSlug) return

    let ws: WebSocket | null = null
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null
    let reconnectAttempts = 0
    let intentionalClose = false

    const connect = () => {
      const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const wsUrl = `${wsProtocol}//${window.location.host}/ws/events`

      ws = new WebSocket(wsUrl)

      ws.onopen = () => {
        setConnected(true)
        reconnectAttempts = 0

        // Subscribe to all channels; server-side auth controls access.
        // Client-side eventTypes filtering is applied in handleEvent.
        const subscribeMsg = {
          type: 'subscribe',
          id: crypto.randomUUID(),
          channels: ['*'],
        }
        ws?.send(JSON.stringify(subscribeMsg))
      }

      ws.onmessage = (event) => {
        try {
          const msg: ServerMessage = JSON.parse(event.data)
          if (msg.type === 'event' && msg.event) {
            const domainEvent: DomainEvent = {
              eventType: msg.event.event_type,
              tenantSlug: msg.event.tenant_id,
              payload: msg.event.payload ?? {},
              timestamp: new Date(msg.event.timestamp),
            }
            handleEventRef.current(domainEvent)
          }
        } catch {
          // Malformed message - silently ignore
        }
      }

      ws.onclose = () => {
        setConnected(false)

        // Reconnect with exponential backoff + jitter unless intentionally closed
        if (!intentionalClose && reconnectAttempts < MAX_RECONNECT_ATTEMPTS) {
          const baseDelay = BASE_RECONNECT_DELAY_MS * Math.pow(2, reconnectAttempts)
          const delay = baseDelay + Math.random() * baseDelay
          reconnectTimeout = setTimeout(() => {
            reconnectAttempts++
            connect()
          }, delay)
        }
      }

      ws.onerror = () => {
        // onclose will fire after onerror; reconnect logic lives there.
        ws?.close()
      }
    }

    connect()

    return () => {
      intentionalClose = true
      if (reconnectTimeout) clearTimeout(reconnectTimeout)
      if (ws) {
        ws.onclose = null // Prevent reconnect on intentional close
        ws.close()
      }
      setConnected(false)
    }
  }, [tenantSlug])

  return {
    /** Whether the WebSocket is currently connected */
    connected,
    /** The last event received, or null if no events yet */
    lastEvent,
  }
}
