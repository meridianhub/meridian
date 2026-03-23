import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockRetrieveAssociations = vi.fn().mockResolvedValue({ associations: [] })
const mockListParticipants = vi.fn().mockResolvedValue({ participants: [] })
const mockRetrieveParty = vi.fn().mockResolvedValue({ party: undefined })

vi.mock('@/api/context', () => ({
  useClients: vi.fn(() => ({
    party: {
      retrieveAssociations: mockRetrieveAssociations,
      listParticipants: mockListParticipants,
      retrieveParty: mockRetrieveParty,
      listParties: vi.fn().mockResolvedValue({ parties: [] }),
      registerAssociations: vi.fn(),
    },
  })),
  useApiClients: vi.fn(() => ({
    party: {
      retrieveAssociations: mockRetrieveAssociations,
      listParticipants: mockListParticipants,
      retrieveParty: mockRetrieveParty,
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

const mockPersonParty = {
  partyId: 'party-person-001',
  legalName: 'Jane Doe',
  partyType: 1, // PARTY_TYPE_PERSON
  status: 1,
}

const mockOrgParty = {
  partyId: 'party-org-001',
  legalName: 'Acme Syndicate',
  partyType: 2, // PARTY_TYPE_ORGANIZATION
  status: 1,
}

const mockAssociation = {
  associationId: 'assoc-001',
  partyId: 'party-person-001',
  relatedPartyId: 'party-org-001',
  relationshipType: 6, // SYNDICATE_PARTICIPANT
  status: 1, // ACTIVE
  metadata: undefined,
  createdAt: undefined,
  updatedAt: undefined,
  effectiveFrom: undefined,
  effectiveTo: undefined,
}

const mockParticipant = {
  associationId: 'assoc-002',
  partyId: 'party-org-001',
  relatedPartyId: 'party-member-001',
  relationshipType: 6, // SYNDICATE_PARTICIPANT
  status: 1, // ACTIVE
  metadata: { allocation_share: '10%' },
  createdAt: undefined,
  updatedAt: undefined,
  effectiveFrom: undefined,
  effectiveTo: undefined,
}

function renderTab(partyId = 'party-001') {
  return render(
    <MemoryRouter>
      <QueryClientProvider client={makeQueryClient()}>
        <TooltipProvider>
          <AssociationsTab partyId={partyId} />
        </TooltipProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('AssociationsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useApiClients).mockReturnValue({
      party: {
        retrieveAssociations: vi.fn().mockResolvedValue({ associations: [] }),
        listParticipants: vi.fn().mockResolvedValue({ participants: [] }),
        retrieveParty: vi.fn().mockResolvedValue({ party: mockPersonParty }),
        listParties: vi.fn().mockResolvedValue({ parties: [] }),
        registerAssociations: vi.fn(),
      },
    } as ReturnType<typeof useApiClients>)
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn(() => new Promise(() => {})),
          listParticipants: vi.fn(() => new Promise(() => {})),
          retrieveParty: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useApiClients>)

      const { container } = renderTab()

      const skeletons = container.querySelectorAll('.animate-pulse')
      expect(skeletons.length).toBeGreaterThan(0)
    })

    it('does not render table while loading', () => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn(() => new Promise(() => {})),
          listParticipants: vi.fn(() => new Promise(() => {})),
          retrieveParty: vi.fn(() => new Promise(() => {})),
        },
      } as ReturnType<typeof useApiClients>)

      renderTab()

      expect(screen.queryByRole('table')).not.toBeInTheDocument()
    })
  })

  describe('empty state - person party', () => {
    beforeEach(() => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({ associations: [] }),
          listParticipants: vi.fn().mockResolvedValue({ participants: [] }),
          retrieveParty: vi.fn().mockResolvedValue({ party: mockPersonParty }),
          listParties: vi.fn().mockResolvedValue({ parties: [] }),
          registerAssociations: vi.fn(),
        },
      } as ReturnType<typeof useApiClients>)
    })

    it('renders empty state for person with no associations', async () => {
      renderTab('party-person-001')

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: /organizations/i })).toBeInTheDocument()
      })
    })

    it('renders descriptive message for person empty state', async () => {
      renderTab('party-person-001')

      await waitFor(() => {
        expect(screen.getByText(/no associations information available/i)).toBeInTheDocument()
      })
    })

    it('renders add association button', async () => {
      renderTab('party-person-001')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add association/i })).toBeInTheDocument()
      })
    })
  })

  describe('empty state - organization party', () => {
    beforeEach(() => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({ associations: [] }),
          listParticipants: vi.fn().mockResolvedValue({ participants: [] }),
          retrieveParty: vi.fn().mockResolvedValue({ party: mockOrgParty }),
          listParties: vi.fn().mockResolvedValue({ parties: [] }),
          registerAssociations: vi.fn(),
        },
      } as ReturnType<typeof useApiClients>)
    })

    it('renders empty state for org with no participants', async () => {
      renderTab('party-org-001')

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: /members/i })).toBeInTheDocument()
      })
    })

    it('renders descriptive message for org empty state', async () => {
      renderTab('party-org-001')

      await waitFor(() => {
        expect(screen.getByText(/no members registered/i)).toBeInTheDocument()
      })
    })
  })

  describe('data state - person party with associations', () => {
    beforeEach(() => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({ associations: [mockAssociation] }),
          listParticipants: vi.fn().mockResolvedValue({ participants: [] }),
          retrieveParty: vi.fn().mockResolvedValue({ party: mockPersonParty }),
          listParties: vi.fn().mockResolvedValue({ parties: [] }),
          registerAssociations: vi.fn(),
        },
      } as ReturnType<typeof useApiClients>)
    })

    it('renders table with association rows', async () => {
      renderTab('party-person-001')

      await waitFor(() => {
        expect(screen.getByRole('table')).toBeInTheDocument()
      })
    })

    it('renders related party as a link', async () => {
      renderTab('party-person-001')

      await waitFor(() => {
        expect(screen.getByRole('link', { name: 'party-org-001' })).toBeInTheDocument()
      })
    })

    it('renders relationship type label', async () => {
      renderTab('party-person-001')

      await waitFor(() => {
        expect(screen.getByText('Syndicate Participant')).toBeInTheDocument()
      })
    })

    it('renders status badge', async () => {
      renderTab('party-person-001')

      await waitFor(() => {
        expect(screen.getByText('ACTIVE')).toBeInTheDocument()
      })
    })
  })

  describe('data state - organization party with participants', () => {
    beforeEach(() => {
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({ associations: [] }),
          listParticipants: vi.fn().mockResolvedValue({ participants: [mockParticipant] }),
          retrieveParty: vi.fn().mockResolvedValue({ party: mockOrgParty }),
          listParties: vi.fn().mockResolvedValue({ parties: [] }),
          registerAssociations: vi.fn(),
        },
      } as ReturnType<typeof useApiClients>)
    })

    it('renders table with participant rows', async () => {
      renderTab('party-org-001')

      await waitFor(() => {
        expect(screen.getByRole('table')).toBeInTheDocument()
      })
    })

    it('renders member party as a link', async () => {
      renderTab('party-org-001')

      await waitFor(() => {
        expect(screen.getByRole('link', { name: 'party-member-001' })).toBeInTheDocument()
      })
    })

    it('renders metadata summary for participant', async () => {
      renderTab('party-org-001')

      await waitFor(() => {
        expect(screen.getByText(/allocation_share/i)).toBeInTheDocument()
      })
    })

    it('calls listParticipants with the org partyId', async () => {
      const listParticipants = vi.fn().mockResolvedValue({ participants: [] })
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations: vi.fn().mockResolvedValue({ associations: [] }),
          listParticipants,
          retrieveParty: vi.fn().mockResolvedValue({ party: mockOrgParty }),
          listParties: vi.fn().mockResolvedValue({ parties: [] }),
          registerAssociations: vi.fn(),
        },
      } as ReturnType<typeof useApiClients>)

      renderTab('party-org-001')

      await waitFor(() => {
        expect(listParticipants).toHaveBeenCalledWith({ partyId: 'party-org-001' })
      })
    })
  })

  describe('query key', () => {
    it('calls retrieveAssociations with the provided partyId', async () => {
      const retrieveAssociations = vi.fn().mockResolvedValue({ associations: [] })
      vi.mocked(useApiClients).mockReturnValue({
        party: {
          retrieveAssociations,
          listParticipants: vi.fn().mockResolvedValue({ participants: [] }),
          retrieveParty: vi.fn().mockResolvedValue({ party: mockPersonParty }),
          listParties: vi.fn().mockResolvedValue({ parties: [] }),
          registerAssociations: vi.fn(),
        },
      } as ReturnType<typeof useApiClients>)

      renderTab('party-abc')

      await waitFor(() => {
        expect(retrieveAssociations).toHaveBeenCalledWith({ partyId: 'party-abc' })
      })
    })
  })
})
