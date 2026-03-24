import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockRetrieveAssociations = vi.fn().mockResolvedValue({})

vi.mock('@/api/context', () => ({
  useClients: vi.fn(() => ({
    party: {
      retrieveAssociations: mockRetrieveAssociations,
    },
  })),
  useApiClients: vi.fn(() => ({
    party: {
      retrieveAssociations: mockRetrieveAssociations,
      listParties: vi.fn().mockResolvedValue({ parties: [] }),
      registerAssociations: vi.fn(),
    },
  })),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
  useCurrentTenant: () => null,
  useIsPlatformAdmin: () => false,
  useSwitchTenant: () => vi.fn(),
  useClearTenant: () => vi.fn(),
}))

import { useApiClients } from '@/api/context'
import { AssociationsTab } from './associations-tab'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
    },
  })
}

const mockPartyClient = {
  retrieveAssociations: vi.fn(),
  listParties: vi.fn().mockResolvedValue({ parties: [] }),
  registerAssociations: vi.fn(),
}

function renderTab(partyId = 'party-001') {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <TooltipProvider>
        <AssociationsTab partyId={partyId} />
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

describe('AssociationsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useApiClients).mockReturnValue({
      party: mockPartyClient,
    } as ReturnType<typeof useApiClients>)
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useApiClients>)

      const { container } = renderTab()

      const skeletons = container.querySelectorAll('.animate-pulse')
      expect(skeletons.length).toBeGreaterThan(0)
    })

    it('does not render empty state while loading', () => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useApiClients>)

      renderTab()

      expect(screen.queryByRole('heading', { name: /associations/i })).not.toBeInTheDocument()
    })
  })

  describe('empty state', () => {
    it('renders empty state heading after data loads', async () => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useApiClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: /associations/i })).toBeInTheDocument()
      })
    })

    it('renders descriptive message', async () => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useApiClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/no associations information available/i)).toBeInTheDocument()
      })
    })

    it('renders add association button', async () => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useApiClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add association/i })).toBeInTheDocument()
      })
    })
  })

  describe('query key', () => {
    it('calls retrieveAssociations with the provided partyId', async () => {
      const retrieveAssociations = vi.fn().mockResolvedValue({})
      vi.mocked(useApiClients).mockReturnValue({
        party: { retrieveAssociations },
      } as ReturnType<typeof useApiClients>)

      renderTab('party-abc')

      await waitFor(() => {
        expect(retrieveAssociations).toHaveBeenCalledWith({ partyId: 'party-abc' })
      })
    })
  })
})
