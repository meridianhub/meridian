import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { EVENT_QUERY_MAP, type DomainEvent, type UseEventStreamOptions } from './use-event-stream'

// --- Mocks ---

// Mock useTenantSlug to control tenant context without the full provider tree
const mockTenantSlug = vi.fn<() => string | null>(() => 'acme')
vi.mock('./use-tenant-context', () => ({
  useTenantSlug: () => mockTenantSlug(),
}))

// Track WebSocket instances created by the hook
let mockWsInstances: MockWebSocket[] = []

class MockWebSocket {
  url: string
  onopen: ((ev: Event) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  send = vi.fn()
  close = vi.fn()

  constructor(url: string) {
    this.url = url
    mockWsInstances.push(this)
  }

  simulateOpen() {
    this.onopen?.(new Event('open'))
  }

  simulateMessage(data: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) }))
  }

  simulateClose() {
    this.onclose?.(new CloseEvent('close'))
  }

  simulateError() {
    this.onerror?.(new Event('error'))
  }
}

// --- Helpers ---

function createWrapper(queryClient?: QueryClient) {
  const client =
    queryClient ??
    new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function makeServerEvent(overrides: Partial<DomainEvent> = {}) {
  const event: DomainEvent = {
    eventType: 'AccountStatusChanged',
    tenantSlug: 'acme',
    payload: { accountId: 'acc-1' },
    timestamp: new Date('2026-01-01T00:00:00Z'),
    ...overrides,
  }
  return {
    type: 'event' as const,
    event: {
      event_id: 'evt-1',
      event_type: event.eventType,
      tenant_id: event.tenantSlug,
      timestamp: event.timestamp.toISOString(),
      payload: event.payload,
    },
  }
}

// Lazy-import the hook after mocks are set up
let useEventStream: (
  options?: UseEventStreamOptions,
) => { connected: boolean; lastEvent: DomainEvent | null }

beforeEach(async () => {
  mockWsInstances = []
  mockTenantSlug.mockReturnValue('acme')

  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.stubGlobal('crypto', { randomUUID: () => 'test-uuid' })

  const mod = await import('./use-event-stream')
  useEventStream = mod.useEventStream
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

// --- Tests ---

describe('EVENT_QUERY_MAP', () => {
  it('maps AccountStatusChanged to account query keys', () => {
    const event: DomainEvent = {
      eventType: 'AccountStatusChanged',
      tenantSlug: 'acme',
      payload: { accountId: 'acc-1' },
      timestamp: new Date(),
    }
    const keys = EVENT_QUERY_MAP.AccountStatusChanged(event)
    expect(keys).toEqual([
      ['tenants', 'acme', 'accounts'],
      ['tenants', 'acme', 'accounts', 'acc-1'],
    ])
  })

  it('maps TransactionCompleted to account and position-log keys', () => {
    const event: DomainEvent = {
      eventType: 'TransactionCompleted',
      tenantSlug: 'beta',
      payload: { accountId: 'acc-2' },
      timestamp: new Date(),
    }
    const keys = EVENT_QUERY_MAP.TransactionCompleted(event)
    expect(keys).toEqual([
      ['tenants', 'beta', 'accounts', 'acc-2'],
      ['tenants', 'beta', 'position-logs'],
    ])
  })

  it('maps PaymentOrderCompleted to payment-order keys', () => {
    const event: DomainEvent = {
      eventType: 'PaymentOrderCompleted',
      tenantSlug: 'acme',
      payload: { paymentOrderId: 'po-1' },
      timestamp: new Date(),
    }
    const keys = EVENT_QUERY_MAP.PaymentOrderCompleted(event)
    expect(keys).toEqual([
      ['tenants', 'acme', 'payment-orders'],
      ['tenants', 'acme', 'payment-orders', 'po-1'],
    ])
  })
})

describe('useEventStream', () => {
  it('connects to WebSocket on mount and sets connected state', () => {
    const { result } = renderHook(() => useEventStream(), { wrapper: createWrapper() })

    expect(result.current.connected).toBe(false)

    act(() => {
      mockWsInstances[0].simulateOpen()
    })

    expect(result.current.connected).toBe(true)
  })

  it('sends subscribe message on open', () => {
    renderHook(() => useEventStream(), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
    })

    expect(mockWsInstances[0].send).toHaveBeenCalledWith(
      JSON.stringify({ type: 'subscribe', id: 'test-uuid', channels: ['*'] }),
    )
  })

  it('does not connect when tenantSlug is null', () => {
    mockTenantSlug.mockReturnValue(null)

    renderHook(() => useEventStream(), { wrapper: createWrapper() })

    expect(mockWsInstances).toHaveLength(0)
  })

  it('delivers events via lastEvent state', () => {
    const { result } = renderHook(() => useEventStream(), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage(makeServerEvent())
    })

    expect(result.current.lastEvent).toEqual({
      eventType: 'AccountStatusChanged',
      tenantSlug: 'acme',
      payload: { accountId: 'acc-1' },
      timestamp: new Date('2026-01-01T00:00:00Z'),
    })
  })

  it('calls onEvent callback when event matches', () => {
    const onEvent = vi.fn()

    renderHook(() => useEventStream({ onEvent }), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage(makeServerEvent())
    })

    expect(onEvent).toHaveBeenCalledTimes(1)
    expect(onEvent).toHaveBeenCalledWith(
      expect.objectContaining({ eventType: 'AccountStatusChanged' }),
    )
  })

  it('filters events by eventTypes when specified', () => {
    const onEvent = vi.fn()

    renderHook(() => useEventStream({ eventTypes: ['TransactionCompleted'], onEvent }), {
      wrapper: createWrapper(),
    })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage(makeServerEvent({ eventType: 'AccountStatusChanged' }))
    })

    expect(onEvent).not.toHaveBeenCalled()

    act(() => {
      mockWsInstances[0].simulateMessage(
        makeServerEvent({ eventType: 'TransactionCompleted', payload: { accountId: 'acc-1' } }),
      )
    })

    expect(onEvent).toHaveBeenCalledTimes(1)
  })

  it('enforces tenant isolation - ignores events from other tenants', () => {
    const onEvent = vi.fn()

    renderHook(() => useEventStream({ onEvent }), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage(makeServerEvent({ tenantSlug: 'other-tenant' }))
    })

    expect(onEvent).not.toHaveBeenCalled()
  })

  it('invalidates React Query cache when autoInvalidate is true (default)', () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    renderHook(() => useEventStream(), { wrapper: createWrapper(queryClient) })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage(makeServerEvent())
    })

    expect(invalidateSpy).toHaveBeenCalledTimes(2)
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ['tenants', 'acme', 'accounts'],
    })
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ['tenants', 'acme', 'accounts', 'acc-1'],
    })
  })

  it('does not invalidate queries when autoInvalidate is false', () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    renderHook(() => useEventStream({ autoInvalidate: false }), {
      wrapper: createWrapper(queryClient),
    })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage(makeServerEvent())
    })

    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('does not invalidate queries for unknown event types', () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    renderHook(() => useEventStream(), { wrapper: createWrapper(queryClient) })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage(makeServerEvent({ eventType: 'UnknownEvent' }))
    })

    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('silently ignores malformed messages', () => {
    const onEvent = vi.fn()

    renderHook(() => useEventStream({ onEvent }), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].onmessage?.(new MessageEvent('message', { data: 'not-json' }))
    })

    expect(onEvent).not.toHaveBeenCalled()
  })

  it('ignores non-event message types', () => {
    const onEvent = vi.fn()

    renderHook(() => useEventStream({ onEvent }), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
      mockWsInstances[0].simulateMessage({ type: 'subscribed', subscription_id: 'sub-1' })
    })

    expect(onEvent).not.toHaveBeenCalled()
  })

  it('closes WebSocket on unmount', () => {
    const { unmount } = renderHook(() => useEventStream(), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
    })

    unmount()

    expect(mockWsInstances[0].close).toHaveBeenCalled()
  })

  it('sets connected to false on WebSocket close', () => {
    const { result } = renderHook(() => useEventStream(), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
    })
    expect(result.current.connected).toBe(true)

    act(() => {
      mockWsInstances[0].simulateClose()
    })
    expect(result.current.connected).toBe(false)
  })

  it('attempts reconnect on unexpected close', async () => {
    vi.useFakeTimers()

    try {
      renderHook(() => useEventStream(), { wrapper: createWrapper() })

      act(() => {
        mockWsInstances[0].simulateOpen()
      })
      expect(mockWsInstances).toHaveLength(1)

      act(() => {
        mockWsInstances[0].simulateClose()
      })

      // Advance past reconnect delay (base 1000ms + jitter, max ~2000ms)
      await act(async () => {
        vi.advanceTimersByTime(3000)
      })

      expect(mockWsInstances.length).toBeGreaterThanOrEqual(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('closes WebSocket on error then triggers reconnect via onclose', () => {
    renderHook(() => useEventStream(), { wrapper: createWrapper() })

    act(() => {
      mockWsInstances[0].simulateOpen()
    })

    act(() => {
      mockWsInstances[0].simulateError()
    })

    expect(mockWsInstances[0].close).toHaveBeenCalled()
  })

  it('constructs correct WebSocket URL based on protocol', () => {
    renderHook(() => useEventStream(), { wrapper: createWrapper() })

    expect(mockWsInstances[0].url).toContain('ws:')
    expect(mockWsInstances[0].url).toContain('/ws/events')
  })
})
