import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

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
      <DemographicsTab partyId={partyId} />
    </QueryClientProvider>,
  )
}

const mockDemographics = {
  partyId: 'party-001',
  email: 'contact@acme.com',
  phoneNumber: '+44 20 1234 5678',
  businessName: 'Acme Corp',
  businessRegistration: 'BRN-12345',
  legalName: 'Acme Corporation Ltd',
  nationality: 'GB',
  taxId: 'TAX-9876',
  website: 'https://acme.com',
  streetAddress: '123 Main Street',
  city: 'London',
  state: 'England',
  postalCode: 'SW1A 1AA',
  country: 'GB',
}

describe('DemographicsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn(() => new Promise(() => {})),
          updateParticipant: vi.fn(),
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
          getParticipant: vi.fn().mockResolvedValue(null),
          updateParticipant: vi.fn(),
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
          updateParticipant: vi.fn(),
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
          getParticipant: vi.fn().mockResolvedValue(mockDemographics),
          updateParticipant: vi.fn(),
        },
      } as ReturnType<typeof useClients>)
    })

    it('renders email', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('contact@acme.com')).toBeInTheDocument()
      })
    })

    it('renders phone number', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('+44 20 1234 5678')).toBeInTheDocument()
      })
    })

    it('renders business name', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Acme Corp')).toBeInTheDocument()
      })
    })

    it('renders city', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('London')).toBeInTheDocument()
      })
    })

    it('renders country', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getAllByText('GB').length).toBeGreaterThan(0)
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
          getParticipant: vi.fn().mockResolvedValue(mockDemographics),
          updateParticipant: vi.fn().mockResolvedValue({}),
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

      expect(screen.getByPlaceholderText(/email/i)).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/phone number/i)).toBeInTheDocument()
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
    it('calls updateParticipant on save', async () => {
      const updateParticipant = vi.fn().mockResolvedValue({})
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn().mockResolvedValue(mockDemographics),
          updateParticipant,
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /edit demographics/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /edit demographics/i }))
      await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

      await waitFor(() => {
        expect(updateParticipant).toHaveBeenCalledWith(
          expect.objectContaining({ partyId: 'party-001' }),
        )
      })
    })

    it('returns to view mode on successful save', async () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getParticipant: vi.fn().mockResolvedValue(mockDemographics),
          updateParticipant: vi.fn().mockResolvedValue({}),
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
          getParticipant: vi.fn().mockResolvedValue(mockDemographics),
          updateParticipant: vi.fn(
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
