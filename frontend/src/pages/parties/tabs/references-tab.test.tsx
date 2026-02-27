import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api/context', () => ({
  useClients: vi.fn(),
}))

import { useClients } from '@/api/context'
import { ReferencesTab } from './references-tab'

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
      <ReferencesTab partyId={partyId} />
    </QueryClientProvider>,
  )
}

describe('ReferencesTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveReference: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useClients>)

      const { container } = renderTab()

      const skeletons = container.querySelectorAll('.animate-pulse')
      expect(skeletons.length).toBeGreaterThan(0)
    })

    it('does not render empty state while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveReference: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      expect(screen.queryByRole('heading', { name: /references/i })).not.toBeInTheDocument()
    })
  })

  describe('empty state', () => {
    it('renders empty state heading after data loads', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveReference: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: /references/i })).toBeInTheDocument()
      })
    })

    it('renders descriptive message', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveReference: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/no references information available/i)).toBeInTheDocument()
      })
    })
  })

  describe('query key', () => {
    it('calls retrieveReference with the provided partyId', async () => {
      const retrieveReference = vi.fn().mockResolvedValue({})
      vi.mocked(useClients).mockReturnValue({
        party: { retrieveReference },
      } as ReturnType<typeof useClients>)

      renderTab('party-xyz')

      await waitFor(() => {
        expect(retrieveReference).toHaveBeenCalledWith({ partyId: 'party-xyz' })
      })
    })
  })
})
