import { describe, it, expect } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { useAuthProviders } from './use-auth-providers'
import type { ReactNode } from 'react'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return function Wrapper({ children }: { children: ReactNode }) {
    return QueryClientProvider({ client: queryClient, children })
  }
}

describe('useAuthProviders', () => {
  it('returns empty data when API returns 404', async () => {
    // Default MSW handler returns 404
    const { result } = renderHook(() => useAuthProviders(), { wrapper: createWrapper() })

    await waitFor(() => {
      expect(result.current.isError).toBe(true)
    })
  })

  it('returns providers when API succeeds', async () => {
    server.use(
      http.get('/api/auth/providers', () => {
        return HttpResponse.json({
          providers: [
            { id: 'local', type: 'password', displayName: 'Email' },
            { id: 'google', type: 'oidc', displayName: 'Google' },
          ],
        })
      }),
    )

    const { result } = renderHook(() => useAuthProviders(), { wrapper: createWrapper() })

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true)
    })

    expect(result.current.data).toHaveLength(2)
    expect(result.current.data?.[1]?.id).toBe('google')
    expect(result.current.data?.[1]?.type).toBe('oidc')
  })
})
