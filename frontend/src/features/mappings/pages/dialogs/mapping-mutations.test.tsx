import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useCreateMapping } from './mapping-mutations'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

import { useApiClients } from '@/api/context'
const mockUseApiClients = vi.mocked(useApiClients)

function makeWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

function makeMockClients(overrides: Record<string, unknown> = {}) {
  return {
    mapping: {
      createMapping: vi.fn(),
      ...overrides,
    },
  } as unknown as ReturnType<typeof useApiClients>
}

describe('useCreateMapping', () => {
  beforeEach(() => {
    mockUseApiClients.mockReturnValue(makeMockClients())
  })

  it('returns a mutation object', () => {
    const { result } = renderHook(() => useCreateMapping(), {
      wrapper: makeWrapper(),
    })

    expect(result.current).toBeDefined()
    expect(result.current.mutateAsync).toBeTypeOf('function')
    expect(result.current.isPending).toBe(false)
  })

  it('calls clients.mapping.createMapping with correct arguments', async () => {
    const mockMapping = { id: 'mapping-1', name: 'My Mapping' }
    const createMapping = vi.fn().mockResolvedValue({ mapping: mockMapping })
    mockUseApiClients.mockReturnValue(makeMockClients({ createMapping }))

    const { result } = renderHook(() => useCreateMapping(), {
      wrapper: makeWrapper(),
    })

    const request = {
      name: 'My Mapping',
      targetService: 'meridian.current_account.v1.CurrentAccountService',
      targetRpc: 'CreateAccount',
      version: 1,
      externalSchema: '{}',
    }

    await result.current.mutateAsync(request)

    expect(createMapping).toHaveBeenCalledOnce()
    expect(createMapping).toHaveBeenCalledWith({
      name: 'My Mapping',
      targetService: 'meridian.current_account.v1.CurrentAccountService',
      targetRpc: 'CreateAccount',
      version: 1,
      externalSchema: '{}',
    })
  })

  it('returns the mapping from the response', async () => {
    const mockMapping = { id: 'mapping-abc', name: 'Stripe Webhook' }
    const createMapping = vi.fn().mockResolvedValue({ mapping: mockMapping })
    mockUseApiClients.mockReturnValue(makeMockClients({ createMapping }))

    const { result } = renderHook(() => useCreateMapping(), {
      wrapper: makeWrapper(),
    })

    const returned = await result.current.mutateAsync({
      name: 'Stripe Webhook',
      targetService: 'meridian.payment_order.v1.PaymentOrderService',
      targetRpc: 'InitiatePaymentOrder',
      version: 2,
      externalSchema: '',
    })

    expect(returned).toEqual(mockMapping)
  })

  it('sets isPending to true while mutation is in flight', async () => {
    let resolveMapping!: (value: unknown) => void
    const pendingPromise = new Promise((resolve) => {
      resolveMapping = resolve
    })
    const createMapping = vi.fn().mockReturnValue(pendingPromise)
    mockUseApiClients.mockReturnValue(makeMockClients({ createMapping }))

    const { result } = renderHook(() => useCreateMapping(), {
      wrapper: makeWrapper(),
    })

    void result.current.mutateAsync({
      name: 'Test',
      targetService: 'svc',
      targetRpc: 'rpc',
      version: 1,
      externalSchema: '',
    })

    await waitFor(() => {
      expect(result.current.isPending).toBe(true)
    })

    resolveMapping({ mapping: { id: 'x' } })

    await waitFor(() => {
      expect(result.current.isPending).toBe(false)
    })
  })

  it('propagates errors from the API', async () => {
    const createMapping = vi.fn().mockRejectedValue(new Error('Network error'))
    mockUseApiClients.mockReturnValue(makeMockClients({ createMapping }))

    const { result } = renderHook(() => useCreateMapping(), {
      wrapper: makeWrapper(),
    })

    await expect(
      result.current.mutateAsync({
        name: 'Test',
        targetService: 'svc',
        targetRpc: 'rpc',
        version: 1,
        externalSchema: '',
      }),
    ).rejects.toThrow('Network error')
  })
})
