import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useEventStream, EVENT_QUERY_MAP, type DomainEvent } from './use-event-stream'
import { TenantProvider } from '@/contexts/tenant-context'
import { AuthProvider } from '@/contexts/auth-context'

const createTestQueryClient = () =>
  new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

const createWrapper = () => {
  const testQueryClient = createTestQueryClient()

  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={testQueryClient}>
      <AuthProvider>
        <TenantProvider>{children}</TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

describe('useEventStream', () => {
  it('should return stable interface with connected and lastEvent', () => {
    const { result } = renderHook(() => useEventStream(), {
      wrapper: createWrapper(),
    })

    expect(result.current).toHaveProperty('connected')
    expect(result.current).toHaveProperty('lastEvent')
    expect(result.current).not.toHaveProperty('error')
  })

  it('should initialize with connected=false', () => {
    const { result } = renderHook(() => useEventStream(), {
      wrapper: createWrapper(),
    })

    expect(result.current.connected).toBe(false)
  })

  it('should initialize with null lastEvent', () => {
    const { result } = renderHook(() => useEventStream(), {
      wrapper: createWrapper(),
    })

    expect(result.current.lastEvent).toBeNull()
  })


  it('should contain expected event types in EVENT_QUERY_MAP', () => {
    const expectedEventTypes = [
      'AccountStatusChanged',
      'TransactionCompleted',
      'PaymentOrderCompleted',
    ]

    expectedEventTypes.forEach((eventType) => {
      expect(EVENT_QUERY_MAP).toHaveProperty(eventType)
    })
  })

  it('EVENT_QUERY_MAP should return array of query keys', () => {
    const mockEvent: DomainEvent = {
      eventType: 'AccountStatusChanged',
      tenantSlug: 'test-tenant',
      payload: { accountId: '123' },
      timestamp: new Date(),
    }

    const queryKeys = EVENT_QUERY_MAP['AccountStatusChanged'](mockEvent)

    expect(Array.isArray(queryKeys)).toBe(true)
    expect(queryKeys.length).toBeGreaterThan(0)
  })

  it('should accept onEvent callback option', () => {
    const onEventMock = vi.fn()

    renderHook(() => useEventStream({ onEvent: onEventMock }), {
      wrapper: createWrapper(),
    })

    expect(onEventMock).not.toHaveBeenCalled()
  })

  it('should accept eventTypes filter option', () => {
    renderHook(() => useEventStream({ eventTypes: ['AccountStatusChanged'] }), {
      wrapper: createWrapper(),
    })

    expect(true).toBe(true)
  })

  it('should accept autoInvalidate option', () => {
    renderHook(() => useEventStream({ autoInvalidate: false }), {
      wrapper: createWrapper(),
    })

    expect(true).toBe(true)
  })
})
