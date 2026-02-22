import { type ReactNode } from 'react'
import { render, type RenderOptions, type RenderResult } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { configureAxe } from 'vitest-axe'
import { AuthProvider, useAuth } from '@/contexts/auth-context'
import { TenantProvider, useTenantContext } from '@/contexts/tenant-context'
import { ApiClientProvider } from '@/api/context'

/**
 * Pre-configured axe instance that skips color-contrast checks.
 * Color contrast requires HTMLCanvasElement which is not available in jsdom.
 * All other WCAG 2.1 AA rules remain active.
 */
export const axe = configureAxe({ rules: { 'color-contrast': { enabled: false } } })

/**
 * Creates a fresh QueryClient for each test to prevent cache pollution.
 * Retries and error logging are disabled to keep test output clean.
 */
export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
        staleTime: 0,
      },
      mutations: {
        retry: false,
      },
    },
    logger: {
      log: () => {},
      warn: () => {},
      error: () => {},
    },
  })
}

interface AllProvidersProps {
  children: ReactNode
  initialToken?: string
  queryClient?: QueryClient
}

function ApiClientBridge({ children }: { children: ReactNode }) {
  const { accessToken } = useAuth()
  const { tenantSlug } = useTenantContext()
  const getToken = () => Promise.resolve(accessToken ?? '')
  return (
    <ApiClientProvider tenantSlug={tenantSlug} getToken={getToken}>
      {children}
    </ApiClientProvider>
  )
}

function AllProviders({ children, initialToken, queryClient }: AllProvidersProps) {
  const client = queryClient ?? createTestQueryClient()
  return (
    <QueryClientProvider client={client}>
      <AuthProvider initialToken={initialToken}>
        <TenantProvider>
          <ApiClientBridge>{children}</ApiClientBridge>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

interface CustomRenderOptions extends Omit<RenderOptions, 'wrapper'> {
  initialToken?: string
  queryClient?: QueryClient
}

/**
 * Custom render function that wraps components in all application providers.
 * Use this for integration tests that need the full provider tree.
 *
 * @example
 * const { getByText } = renderWithProviders(<MyComponent />, {
 *   initialToken: createTestToken({ userId: 'user-123' })
 * })
 */
export function renderWithProviders(
  ui: React.ReactElement,
  { initialToken, queryClient, ...renderOptions }: CustomRenderOptions = {},
): RenderResult {
  return render(ui, {
    wrapper: ({ children }) => (
      <AllProviders initialToken={initialToken} queryClient={queryClient}>
        {children}
      </AllProviders>
    ),
    ...renderOptions,
  })
}

export * from '@testing-library/react'
