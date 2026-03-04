import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: vi.fn(),
}))

import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { useInitiatePayment, useCancelPayment, useReversePayment } from './payment-mutations'

function makeWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const mockPaymentOrder = {
  paymentOrderId: 'po-123',
  debtorAccountId: 'acc-456',
  creditorReference: 'GB29NWBK60161331926819',
  amount: '10000',
  currency: 'GBP',
  status: 'COMPLETED',
  createdAt: { seconds: BigInt(1700000000), nanos: 0 },
}

describe('useInitiatePayment', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useTenantSlug).mockReturnValue('acme-bank')
  })

  it('calls initiatePaymentOrder with correct params and returns paymentOrder', async () => {
    const initiatePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { initiatePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useInitiatePayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        debtorAccountId: 'acc-456',
        creditorReference: 'cred-ref-001',
        amount: '100.00',
        currency: 'GBP',
      })
    })

    expect(initiatePaymentOrder).toHaveBeenCalledOnce()
    const callArg = initiatePaymentOrder.mock.calls[0][0]
    expect(callArg.debtorAccountId).toBe('acc-456')
    expect(callArg.creditorReference).toBe('cred-ref-001')
    expect(callArg.amount.units).toBe('10000')
    expect(callArg.amount.currency).toBe('GBP')
    expect(callArg.idempotencyKey.key).toBeDefined()

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual(mockPaymentOrder)
  })

  it('converts amount string to BigInt minor units', async () => {
    const initiatePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { initiatePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useInitiatePayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        debtorAccountId: 'acc-456',
        creditorReference: 'cred-ref-001',
        amount: '250.75',
        currency: 'USD',
      })
    })

    const callArg = initiatePaymentOrder.mock.calls[0][0]
    expect(callArg.amount.units).toBe('25075')
    expect(callArg.amount.currency).toBe('USD')
  })

  it('generates unique idempotency keys on each call', async () => {
    const initiatePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { initiatePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useInitiatePayment(), { wrapper: makeWrapper() })

    const request = {
      debtorAccountId: 'acc-456',
      creditorReference: 'cred-ref-001',
      amount: '100.00',
      currency: 'GBP',
    }

    await act(async () => {
      await result.current.mutateAsync(request)
    })
    await act(async () => {
      await result.current.mutateAsync(request)
    })

    const key1 = initiatePaymentOrder.mock.calls[0][0].idempotencyKey.key
    const key2 = initiatePaymentOrder.mock.calls[1][0].idempotencyKey.key
    expect(key1).toBeDefined()
    expect(key2).toBeDefined()
    expect(key1).not.toBe(key2)
  })

  it('enters error state when API fails', async () => {
    const initiatePaymentOrder = vi.fn().mockRejectedValue(new Error('Network error'))
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { initiatePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useInitiatePayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        debtorAccountId: 'acc-456',
        creditorReference: 'cred-ref-001',
        amount: '100.00',
        currency: 'GBP',
      }).catch(() => {})
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeInstanceOf(Error)
  })

  it('invalidates payments query on success when tenantSlug is set', async () => {
    const initiatePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { initiatePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useInitiatePayment(), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({
        debtorAccountId: 'acc-456',
        creditorReference: 'cred-ref-001',
        amount: '100.00',
        currency: 'GBP',
      })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(invalidateQueries).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: expect.arrayContaining(['tenants', 'acme-bank', 'payments']) }),
    )
  })

  it('does not invalidate queries when tenantSlug is null', async () => {
    vi.mocked(useTenantSlug).mockReturnValue(null)
    const initiatePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { initiatePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useInitiatePayment(), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({
        debtorAccountId: 'acc-456',
        creditorReference: 'cred-ref-001',
        amount: '100.00',
        currency: 'GBP',
      })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(invalidateQueries).not.toHaveBeenCalled()
  })
})

describe('useCancelPayment', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useTenantSlug).mockReturnValue('acme-bank')
  })

  it('calls cancelPaymentOrder with correct params and returns paymentOrder', async () => {
    const cancelPaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { cancelPaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useCancelPayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        cancellationReason: 'Customer request',
        cancelledBy: 'admin-user',
      })
    })

    expect(cancelPaymentOrder).toHaveBeenCalledOnce()
    const callArg = cancelPaymentOrder.mock.calls[0][0]
    expect(callArg.paymentOrderId).toBe('po-123')
    expect(callArg.cancellationReason).toBe('Customer request')
    expect(callArg.cancelledBy).toBe('admin-user')
    expect(callArg.idempotencyKey.key).toBeDefined()

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual(mockPaymentOrder)
  })

  it('defaults cancelledBy to "operations-console" when not provided', async () => {
    const cancelPaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { cancelPaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useCancelPayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        cancellationReason: 'Test reason',
      })
    })

    const callArg = cancelPaymentOrder.mock.calls[0][0]
    expect(callArg.cancelledBy).toBe('operations-console')
  })

  it('enters error state when API fails', async () => {
    const cancelPaymentOrder = vi.fn().mockRejectedValue(new Error('Cancel failed'))
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { cancelPaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useCancelPayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        cancellationReason: 'Test',
      }).catch(() => {})
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeInstanceOf(Error)
  })

  it('invalidates both payment and payments queries on success', async () => {
    const cancelPaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { cancelPaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useCancelPayment(), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        cancellationReason: 'Test',
      })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(invalidateQueries).toHaveBeenCalledTimes(2)
    expect(invalidateQueries).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: expect.arrayContaining(['tenants', 'acme-bank', 'payments', 'po-123']) }),
    )
    expect(invalidateQueries).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: expect.arrayContaining(['tenants', 'acme-bank', 'payments']) }),
    )
  })

  it('does not invalidate queries when tenantSlug is null', async () => {
    vi.mocked(useTenantSlug).mockReturnValue(null)
    const cancelPaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { cancelPaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useCancelPayment(), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        cancellationReason: 'Test',
      })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(invalidateQueries).not.toHaveBeenCalled()
  })
})

describe('useReversePayment', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useTenantSlug).mockReturnValue('acme-bank')
  })

  it('calls reversePaymentOrder with correct params and returns paymentOrder', async () => {
    const reversePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { reversePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useReversePayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        reversalReason: 'Duplicate payment',
        reversedBy: 'admin-user',
      })
    })

    expect(reversePaymentOrder).toHaveBeenCalledOnce()
    const callArg = reversePaymentOrder.mock.calls[0][0]
    expect(callArg.paymentOrderId).toBe('po-123')
    expect(callArg.reversalReason).toBe('Duplicate payment')
    expect(callArg.reversedBy).toBe('admin-user')
    expect(callArg.idempotencyKey.key).toBeDefined()

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual(mockPaymentOrder)
  })

  it('defaults reversedBy to "operations-console" when not provided', async () => {
    const reversePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { reversePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useReversePayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        reversalReason: 'Test reason',
      })
    })

    const callArg = reversePaymentOrder.mock.calls[0][0]
    expect(callArg.reversedBy).toBe('operations-console')
  })

  it('enters error state when API fails', async () => {
    const reversePaymentOrder = vi.fn().mockRejectedValue(new Error('Reversal failed'))
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { reversePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const { result } = renderHook(() => useReversePayment(), { wrapper: makeWrapper() })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        reversalReason: 'Test',
      }).catch(() => {})
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeInstanceOf(Error)
  })

  it('invalidates both payment and payments queries on success', async () => {
    const reversePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { reversePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useReversePayment(), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        reversalReason: 'Test',
      })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(invalidateQueries).toHaveBeenCalledTimes(2)
    expect(invalidateQueries).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: expect.arrayContaining(['tenants', 'acme-bank', 'payments', 'po-123']) }),
    )
    expect(invalidateQueries).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: expect.arrayContaining(['tenants', 'acme-bank', 'payments']) }),
    )
  })

  it('does not invalidate queries when tenantSlug is null', async () => {
    vi.mocked(useTenantSlug).mockReturnValue(null)
    const reversePaymentOrder = vi.fn().mockResolvedValue({ paymentOrder: mockPaymentOrder })
    vi.mocked(useApiClients).mockReturnValue({
      paymentOrder: { reversePaymentOrder },
    } as unknown as ReturnType<typeof useApiClients>)

    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useReversePayment(), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({
        paymentOrderId: 'po-123',
        reversalReason: 'Test',
      })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(invalidateQueries).not.toHaveBeenCalled()
  })
})
