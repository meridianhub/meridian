import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'

vi.mock('@/api/context', () => ({
  useClients: vi.fn(),
}))

import { useClients } from '@/api/context'
import { OverviewTab } from './overview-tab'

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
      <TooltipProvider>
        <OverviewTab partyId={partyId} />
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

const mockParty = {
  partyId: 'party-001',
  name: 'Acme Corp',
  partyType: 'ORGANIZATION',
  status: 'ACTIVE',
  externalReference: 'EXT-123',
  verificationStatus: 'VERIFIED',
  createdAt: { seconds: 1700000000, nanos: 0 },
  updatedAt: { seconds: 1700001000, nanos: 0 },
}

describe('OverviewTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useClients>)

      const { container } = renderTab()

      const skeletons = container.querySelectorAll('.animate-pulse')
      expect(skeletons.length).toBeGreaterThan(0)
    })
  })

  describe('empty state', () => {
    it('renders empty state when no party data is returned', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn().mockResolvedValue(null),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/no data/i)).toBeInTheDocument()
      })
    })

    it('shows descriptive message in empty state', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn().mockResolvedValue(null),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/party information not found/i)).toBeInTheDocument()
      })
    })
  })

  describe('data state', () => {
    beforeEach(() => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn().mockResolvedValue(mockParty),
        },
      } as ReturnType<typeof useClients>)
    })

    it('renders party ID', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('party-001')).toBeInTheDocument()
      })
    })

    it('renders party name', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Acme Corp')).toBeInTheDocument()
      })
    })

    it('renders party type', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('ORGANIZATION')).toBeInTheDocument()
      })
    })

    it('renders party status', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('ACTIVE')).toBeInTheDocument()
      })
    })

    it('renders external reference', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('EXT-123')).toBeInTheDocument()
      })
    })

    it('renders verification status', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('VERIFIED')).toBeInTheDocument()
      })
    })

    it('renders TimeDisplay for createdAt timestamp', async () => {
      renderTab()
      await waitFor(() => {
        // TimeDisplay renders a time representation; just verify the Created label is present
        expect(screen.getByText('Created')).toBeInTheDocument()
      })
    })

    it('renders TimeDisplay for updatedAt timestamp', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Updated')).toBeInTheDocument()
      })
    })
  })

  describe('missing optional fields', () => {
    it('renders em dash for missing externalReference', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn().mockResolvedValue({
            partyId: 'party-001',
            name: 'Acme Corp',
            partyType: 'ORGANIZATION',
            status: 'ACTIVE',
          }),
        },
      } as ReturnType<typeof useClients>)

      const { getAllByText } = renderTab()
      await waitFor(() => {
        expect(getAllByText('—').length).toBeGreaterThan(0)
      })
    })

    it('renders em dash for missing timestamps', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn().mockResolvedValue({
            partyId: 'party-001',
            name: 'Acme Corp',
            partyType: 'ORGANIZATION',
            status: 'ACTIVE',
          }),
        },
      } as ReturnType<typeof useClients>)

      const { getAllByText } = renderTab()
      await waitFor(() => {
        // 4 dashes: externalReference, verificationStatus, createdAt, updatedAt
        expect(getAllByText('—').length).toBeGreaterThanOrEqual(4)
      })
    })
  })
})
