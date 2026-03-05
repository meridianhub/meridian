import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { StarlarkConfigPage } from './index'

// Mock useApiClients so we can inject test clients
vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
  useCurrentTenant: () => null,
  useIsPlatformAdmin: () => false,
  useSwitchTenant: () => vi.fn(),
  useClearTenant: () => vi.fn(),
}))

import { useApiClients } from '@/api/context'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  )
}

// Sample saga definitions
const platformSaga = {
  id: 'saga-1',
  name: 'current_account_withdrawal',
  version: 1,
  script: 'def saga(): pass',
  status: 2, // ACTIVE
  isSystem: true,
  displayName: 'Current Account Withdrawal',
  description: 'Handles withdrawals',
  createdAt: undefined,
  updatedAt: { seconds: BigInt(1707000000), nanos: 0 },
  activatedAt: undefined,
  deprecatedAt: undefined,
  successorId: '',
  preconditionsExpression: '',
}

const tenantSaga = {
  id: 'saga-2',
  name: 'payment_initiation',
  version: 2,
  script: 'def saga(): pass',
  status: 1, // DRAFT
  isSystem: false,
  displayName: 'Payment Initiation',
  description: 'Initiates a payment',
  createdAt: undefined,
  updatedAt: { seconds: BigInt(1707000001), nanos: 0 },
  activatedAt: undefined,
  deprecatedAt: undefined,
  successorId: '',
  preconditionsExpression: '',
}

const deprecatedSaga = {
  id: 'saga-3',
  name: 'old_transfer',
  version: 1,
  script: 'def saga(): pass',
  status: 3, // DEPRECATED
  isSystem: false,
  displayName: 'Old Transfer',
  description: 'Legacy transfer',
  createdAt: undefined,
  updatedAt: { seconds: BigInt(1707000002), nanos: 0 },
  activatedAt: undefined,
  deprecatedAt: undefined,
  successorId: '',
  preconditionsExpression: '',
}

function makeMockClients(sagas = [platformSaga, tenantSaga, deprecatedSaga]) {
  return {
    sagaRegistry: {
      listSagas: vi.fn().mockResolvedValue({ sagas, nextPageToken: '' }),
    },
  }
}

describe('StarlarkConfigPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering', () => {
    it('renders page title and description', () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      expect(screen.getByRole('heading', { name: /Starlark Configuration/i })).toBeInTheDocument()
    })

    it('renders DataTable with all expected columns', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Version/i })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Status/i })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Display Name/i })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Updated/i })).toBeInTheDocument()
      })
    })

    it('displays saga definitions in table', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('current_account_withdrawal')).toBeInTheDocument()
        expect(screen.getByText('payment_initiation')).toBeInTheDocument()
        expect(screen.getByText('old_transfer')).toBeInTheDocument()
      })
    })

    it('shows empty state when no sagas returned', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients([]) as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByTestId('empty-state')).toBeInTheDocument()
      })
    })
  })

  describe('platform admin view', () => {
    it('shows override counts column for platform admin', async () => {
      // We need to test this with a platform admin auth context
      // Platform admin sees extra column for override counts
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage isPlatformAdmin={true} />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByRole('columnheader', { name: /Overrides/i })).toBeInTheDocument()
      })
    })

    it('does not show override counts column for tenant admin', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage isPlatformAdmin={false} />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.queryByRole('columnheader', { name: /Overrides/i })).not.toBeInTheDocument()
      })
    })
  })

  describe('tenant admin view', () => {
    it('shows source column for tenant admin (platform default vs tenant override)', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage isPlatformAdmin={false} />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByRole('columnheader', { name: /Source/i })).toBeInTheDocument()
      })
    })

    it('displays "Platform Default" source for system sagas', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients([platformSaga]) as never)

      render(
        <Wrapper>
          <StarlarkConfigPage isPlatformAdmin={false} />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('Platform Default')).toBeInTheDocument()
      })
    })

    it('displays "Tenant Override" source for non-system sagas', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients([tenantSaga]) as never)

      render(
        <Wrapper>
          <StarlarkConfigPage isPlatformAdmin={false} />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('Tenant Override')).toBeInTheDocument()
      })
    })
  })

  describe('status badges', () => {
    it('renders status badges for each saga', async () => {
      vi.mocked(useApiClients).mockReturnValue(
        makeMockClients([platformSaga, tenantSaga, deprecatedSaga]) as never,
      )

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('ACTIVE')).toBeInTheDocument()
        expect(screen.getByText('DRAFT')).toBeInTheDocument()
        expect(screen.getByText('DEPRECATED')).toBeInTheDocument()
      })
    })
  })

  describe('row navigation', () => {
    it('renders rows as links to detail pages', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients([platformSaga]) as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        const nameLink = screen.getByRole('link', { name: /current_account_withdrawal/i })
        expect(nameLink).toBeInTheDocument()
        expect(nameLink).toHaveAttribute('href', '/starlark-config/saga-1')
      })
    })
  })

  describe('error handling', () => {
    it('shows loading skeleton while fetching', () => {
      vi.mocked(useApiClients).mockReturnValue({
        sagaRegistry: {
          listSagas: vi.fn().mockReturnValue(new Promise(() => {})),
        },
      } as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
    })

    it('shows retry button on error', async () => {
      vi.mocked(useApiClients).mockReturnValue({
        sagaRegistry: {
          listSagas: vi.fn().mockRejectedValue(new Error('Network error')),
        },
      } as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()
      })
    })
  })
})
