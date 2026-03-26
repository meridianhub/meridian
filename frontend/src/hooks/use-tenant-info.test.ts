import { describe, it, expect } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { useTenantInfo } from './use-tenant-info'
import type { ReactNode } from 'react'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return function Wrapper({ children }: { children: ReactNode }) {
    return QueryClientProvider({ client: queryClient, children })
  }
}

describe('useTenantInfo', () => {
  it('returns null when API returns 404 (bare domain)', async () => {
    // Default MSW handler returns 404
    const { result } = renderHook(() => useTenantInfo(), { wrapper: createWrapper() })

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })

    expect(result.current.displayName).toBeNull()
    expect(result.current.slug).toBeNull()
  })

  it('returns tenant info when API succeeds', async () => {
    server.use(
      http.get('/api/tenant-info', () => {
        return HttpResponse.json({
          slug: 'volterra-energy',
          displayName: 'Volterra Energy',
        })
      }),
    )

    const { result } = renderHook(() => useTenantInfo(), { wrapper: createWrapper() })

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })

    expect(result.current.displayName).toBe('Volterra Energy')
    expect(result.current.slug).toBe('volterra-energy')
  })

  it('returns null when API returns server error', async () => {
    server.use(
      http.get('/api/tenant-info', () => {
        return HttpResponse.json({}, { status: 500 })
      }),
    )

    const { result } = renderHook(() => useTenantInfo(), { wrapper: createWrapper() })

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })

    expect(result.current.displayName).toBeNull()
  })
})
