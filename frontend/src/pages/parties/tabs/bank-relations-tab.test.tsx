import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api/context', () => ({
  useClients: vi.fn(),
}))

import { useClients } from '@/api/context'
import { BankRelationsTab } from './bank-relations-tab'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
    },
  })
}

function renderTab(partyId = 'party-001') {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <BankRelationsTab partyId={partyId} />
    </QueryClientProvider>,
  )
}

describe('BankRelationsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getBankRelations: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useClients>)

      const { container } = renderTab()

      const skeletons = container.querySelectorAll('.animate-pulse')
      expect(skeletons.length).toBeGreaterThan(0)
    })

    it('does not render empty state while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getBankRelations: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      expect(screen.queryByRole('heading', { name: /bank relations/i })).not.toBeInTheDocument()
    })
  })

  describe('empty state', () => {
    it('renders empty state heading after data loads', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getBankRelations: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: /bank relations/i })).toBeInTheDocument()
      })
    })

    it('renders descriptive message', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getBankRelations: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/no bank relations information available/i)).toBeInTheDocument()
      })
    })
  })

  describe('query key', () => {
    it('calls getBankRelations with the provided partyId', async () => {
      const getBankRelations = vi.fn().mockResolvedValue({})
      vi.mocked(useClients).mockReturnValue({
        party: { getBankRelations },
      } as ReturnType<typeof useClients>)

      renderTab('party-xyz')

      await waitFor(() => {
        expect(getBankRelations).toHaveBeenCalledWith({ partyId: 'party-xyz' })
      })
    })
  })
})
