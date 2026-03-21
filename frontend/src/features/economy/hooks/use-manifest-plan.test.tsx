import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useManifestPlan } from './use-manifest-plan'

const mockApplyManifest = vi.fn()

vi.mock('@/api/context', () => ({
  useApiClients: () => ({
    manifestApplier: {
      applyManifest: mockApplyManifest,
    },
  }),
}))

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

describe('useManifestPlan', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('returns initial state with null plan', () => {
    const { result } = renderHook(() => useManifestPlan(), { wrapper: createWrapper() })

    expect(result.current.plan).toBeNull()
    expect(result.current.isPlanning).toBe(false)
    expect(result.current.error).toBeNull()
    expect(typeof result.current.planManifest).toBe('function')
    expect(typeof result.current.planManifestAsync).toBe('function')
  })

  it('calls applyManifest with dryRun=true and maps response', async () => {
    const mockResponse = {
      status: 1,
      diffSummary: '3 add, 2 modify, 1 remove',
      stepResults: [{ stepName: 'step1', status: 1 }],
      validationErrors: [{ severity: 'WARNING', message: 'warn' }],
    }
    mockApplyManifest.mockResolvedValue(mockResponse)

    const { result } = renderHook(() => useManifestPlan(), { wrapper: createWrapper() })

    act(() => {
      result.current.planManifest({} as never)
    })

    await waitFor(() => {
      expect(result.current.plan).not.toBeNull()
    })

    expect(mockApplyManifest).toHaveBeenCalledWith({
      manifest: {},
      dryRun: true,
      force: false,
      appliedBy: '',
    })

    expect(result.current.plan).toEqual({
      status: 1,
      diffSummary: '3 add, 2 modify, 1 remove',
      stepResults: [{ stepName: 'step1', status: 1 }],
      validationErrors: [{ severity: 'WARNING', message: 'warn' }],
      counts: { add: 3, modify: 2, remove: 1 },
    })
  })

  it('parses diff counts with various formats', async () => {
    mockApplyManifest.mockResolvedValue({
      status: 1,
      diffSummary: '10 additions, 5 modifications, 2 deletions',
      stepResults: [],
      validationErrors: [],
    })

    const { result } = renderHook(() => useManifestPlan(), { wrapper: createWrapper() })

    act(() => {
      result.current.planManifest({} as never)
    })

    await waitFor(() => {
      expect(result.current.plan).not.toBeNull()
    })

    expect(result.current.plan!.counts).toEqual({ add: 10, modify: 5, remove: 2 })
  })

  it('returns zeros for empty diff summary', async () => {
    mockApplyManifest.mockResolvedValue({
      status: 1,
      diffSummary: 'No changes',
      stepResults: [],
      validationErrors: [],
    })

    const { result } = renderHook(() => useManifestPlan(), { wrapper: createWrapper() })

    act(() => {
      result.current.planManifest({} as never)
    })

    await waitFor(() => {
      expect(result.current.plan).not.toBeNull()
    })

    expect(result.current.plan!.counts).toEqual({ add: 0, modify: 0, remove: 0 })
  })

  it('sets error on failure', async () => {
    mockApplyManifest.mockRejectedValue(new Error('Plan failed'))

    const { result } = renderHook(() => useManifestPlan(), { wrapper: createWrapper() })

    act(() => {
      result.current.planManifest({} as never)
    })

    await waitFor(() => {
      expect(result.current.error).not.toBeNull()
    })

    expect(result.current.error).toBeInstanceOf(Error)
    expect((result.current.error as Error).message).toBe('Plan failed')
    expect(result.current.plan).toBeNull()
  })
})
