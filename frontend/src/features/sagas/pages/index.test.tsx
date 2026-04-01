import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { StarlarkConfigPage } from './index'

// Mock useApiClients so we can inject test clients
vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

vi.mock('@/lib/analytics', () => ({
  track: vi.fn(),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
  useCurrentTenant: () => null,
  useIsPlatformAdmin: () => false,
  useSwitchTenant: () => vi.fn(),
  useClearTenant: () => vi.fn(),
}))

import { useApiClients } from '@/api/context'
import { track } from '@/lib/analytics'

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

// Sample manifest saga definitions (from control_plane/v1/manifest_pb)
const eventSaga = {
  name: 'current_account_withdrawal',
  trigger: 'event:position-keeping.transaction-captured.v1',
  script: 'def saga(): pass',
  filter: 'event.amount > 0',
}

const apiSaga = {
  name: 'payment_initiation',
  trigger: 'api:/v1/payments',
  script: 'def saga(): pass',
}

const scheduledSaga = {
  name: 'daily_reconciliation',
  trigger: 'scheduled:daily_reconciliation',
  script: 'def saga(): pass',
}

function makeMockClients(sagas = [eventSaga, apiSaga, scheduledSaga]) {
  return {
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({
        version: {
          manifest: { sagas },
        },
      }),
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

    it('renders DataTable with expected columns', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Trigger/i })).toBeInTheDocument()
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
        expect(screen.getByText('daily_reconciliation')).toBeInTheDocument()
      })
    })

    it('shows empty state when no sagas in manifest', async () => {
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

    it('shows empty state when no manifest exists', async () => {
      vi.mocked(useApiClients).mockReturnValue({
        manifestHistory: {
          getCurrentManifest: vi.fn().mockResolvedValue({ version: null }),
        },
      } as never)

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

  describe('trigger display', () => {
    it('shows trigger type badges', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('event')).toBeInTheDocument()
        expect(screen.getByText('api')).toBeInTheDocument()
        expect(screen.getByText('scheduled')).toBeInTheDocument()
      })
    })
  })

  describe('row navigation', () => {
    it('renders rows as links using saga name', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients([eventSaga]) as never)

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        const nameLink = screen.getByRole('link', { name: /current_account_withdrawal/i })
        expect(nameLink).toBeInTheDocument()
        expect(nameLink).toHaveAttribute('href', '/starlark-config/current_account_withdrawal')
      })
    })
  })

  describe('error handling', () => {
    it('shows loading skeleton while fetching', () => {
      vi.mocked(useApiClients).mockReturnValue({
        manifestHistory: {
          getCurrentManifest: vi.fn().mockReturnValue(new Promise(() => {})),
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
        manifestHistory: {
          getCurrentManifest: vi.fn().mockRejectedValue(new Error('Network error')),
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

  describe('Platform badge', () => {
    it('renders Platform badge for isSystem sagas', async () => {
      vi.mocked(useApiClients).mockReturnValue(
        makeMockClients([
          { ...eventSaga, isSystem: true },
          { ...apiSaga, isSystem: false },
        ]) as never,
      )

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('current_account_withdrawal')).toBeInTheDocument()
      })

      const badges = screen.getAllByText('Platform')
      expect(badges).toHaveLength(1)
    })

    it('does not render Platform badge for non-system sagas', async () => {
      vi.mocked(useApiClients).mockReturnValue(
        makeMockClients([{ ...apiSaga, isSystem: false }]) as never,
      )

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('payment_initiation')).toBeInTheDocument()
      })

      expect(screen.queryByText('Platform')).not.toBeInTheDocument()
    })
  })

  describe('analytics', () => {
    it('fires platform_badge_visible when system sagas are present', async () => {
      vi.mocked(useApiClients).mockReturnValue(
        makeMockClients([
          { ...eventSaga, isSystem: true },
          { ...apiSaga, isSystem: false },
        ]) as never,
      )

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(vi.mocked(track)).toHaveBeenCalledWith('economy.platform_badge_visible', {
          page: 'sagas',
          platform_count: 1,
          tenant_count: 1,
        })
      })
    })

    it('does not fire platform_badge_visible when no system sagas', async () => {
      vi.mocked(useApiClients).mockReturnValue(
        makeMockClients([{ ...apiSaga, isSystem: false }]) as never,
      )

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('payment_initiation')).toBeInTheDocument()
      })

      expect(vi.mocked(track)).not.toHaveBeenCalledWith(
        'economy.platform_badge_visible',
        expect.anything(),
      )
    })

    it('fires platform_resource_clicked when system saga link is clicked', async () => {
      vi.mocked(useApiClients).mockReturnValue(
        makeMockClients([{ ...eventSaga, isSystem: true }]) as never,
      )

      const user = userEvent.setup()

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('current_account_withdrawal')).toBeInTheDocument()
      })

      await user.click(screen.getByRole('link', { name: /current_account_withdrawal/i }))

      expect(vi.mocked(track)).toHaveBeenCalledWith('economy.platform_resource_clicked', {
        resource_type: 'saga',
        resource_code: 'current_account_withdrawal',
        page: 'sagas',
      })
    })

    it('fires override_intent when Create Saga button is clicked', async () => {
      vi.mocked(useApiClients).mockReturnValue(makeMockClients() as never)

      const user = userEvent.setup()

      render(
        <Wrapper>
          <StarlarkConfigPage />
        </Wrapper>,
      )

      await user.click(screen.getByRole('button', { name: /Create Saga/i }))

      expect(vi.mocked(track)).toHaveBeenCalledWith('economy.override_intent', {
        source_saga_name: '',
        navigation_path: '/starlark-config/new',
      })
    })
  })
})
