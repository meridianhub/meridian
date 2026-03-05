import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'

vi.mock('@/api/context', () => ({
  useClients: vi.fn(),
}))

import { useClients } from '@/api/context'
import { DemographicsTab } from './demographics-tab'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  })
}

function renderTab(partyId = 'party-001') {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <TooltipProvider>
        <DemographicsTab partyId={partyId} />
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

const mockDemographics = {
  partyId: 'party-001',
  socioEconomicData: 'Middle income bracket',
  employmentHistory: 'Software Engineer at Acme Corp',
  updatedAt: { seconds: 1700001000, nanos: 0 },
}

describe('DemographicsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveDemographics: vi.fn(() => new Promise(() => {})),
          updateDemographics: vi.fn(),
        },
      } as ReturnType<typeof useClients>)

      const { container } = renderTab()

      const skeletons = container.querySelectorAll('.animate-pulse')
      expect(skeletons.length).toBeGreaterThan(0)
    })
  })

  describe('empty state', () => {
    it('renders empty state when no demographics data is returned', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveDemographics: vi.fn().mockResolvedValue(null),
          updateDemographics: vi.fn(),
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
          retrieveDemographics: vi.fn().mockResolvedValue(null),
          updateDemographics: vi.fn(),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/demographics information not found/i)).toBeInTheDocument()
      })
    })
  })

  describe('view mode (data state)', () => {
    beforeEach(() => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveDemographics: vi.fn().mockResolvedValue(mockDemographics),
          updateDemographics: vi.fn(),
        },
      } as ReturnType<typeof useClients>)
    })

    it('renders socio-economic data', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Middle income bracket')).toBeInTheDocument()
      })
    })

    it('renders employment history', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Software Engineer at Acme Corp')).toBeInTheDocument()
      })
    })

    it('renders Edit Demographics button', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })
    })
  })

  describe('edit mode toggle', () => {
    beforeEach(() => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveDemographics: vi.fn().mockResolvedValue(mockDemographics),
          updateDemographics: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useClients>)
    })

    it('switches to edit mode when Edit Demographics is clicked', async () => {
      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /edit demographics/i }))

      expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    })

    it('shows input fields in edit mode', async () => {
      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /edit demographics/i }))

      expect(screen.getByPlaceholderText(/socio-economic/i)).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/employment/i)).toBeInTheDocument()
    })

    it('returns to view mode when Cancel is clicked', async () => {
      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /edit demographics/i }))
      expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument()

      await userEvent.click(screen.getByRole('button', { name: /cancel/i }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })
    })
  })

  describe('form submission', () => {
    it('calls updateDemographics on save', async () => {
      const updateDemographics = vi.fn().mockResolvedValue({})
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveDemographics: vi.fn().mockResolvedValue(mockDemographics),
          updateDemographics,
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /edit demographics/i }))
      await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

      await waitFor(() => {
        expect(updateDemographics).toHaveBeenCalledWith(
          expect.objectContaining({ partyId: 'party-001' }),
        )
      })
    })

    it('returns to view mode on successful save', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveDemographics: vi.fn().mockResolvedValue(mockDemographics),
          updateDemographics: vi.fn().mockResolvedValue({}),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /edit demographics/i }))
      await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })
    })

    it('disables Save button while mutation is pending', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          retrieveDemographics: vi.fn().mockResolvedValue(mockDemographics),
          updateDemographics: vi.fn(
            () => new Promise((resolve) => setTimeout(resolve, 200)),
          ),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /edit demographics/i }))
      await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

      expect(screen.getByRole('button', { name: /save changes/i })).toBeDisabled()
    })
  })
})
