import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PartyHeader } from './party-header'

const mockRetrieveParty = vi.fn()

vi.mock('@/api/context', () => ({
  useClients: vi.fn(() => ({
    party: {
      retrieveParty: mockRetrieveParty,
    },
  })),
}))

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
    },
  })
}

function renderPartyHeader(partyId = 'test-party-1') {
  const qc = makeQueryClient()
  return render(
    <QueryClientProvider client={qc}>
      <PartyHeader partyId={partyId} />
    </QueryClientProvider>,
  )
}

describe('PartyHeader - loading state', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRetrieveParty.mockReturnValue(new Promise(() => {})) // never resolves
  })

  it('renders skeleton placeholders while loading', () => {
    renderPartyHeader()
    const container = document.querySelector('.space-y-4')
    expect(container).toBeInTheDocument()
  })

  it('does not show party name while loading', () => {
    renderPartyHeader()
    expect(screen.queryByRole('heading')).not.toBeInTheDocument()
  })
})

describe('PartyHeader - error/not found state', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRetrieveParty.mockResolvedValue({ party: undefined })
  })

  it('shows "Party not found" when party data is undefined', async () => {
    renderPartyHeader()
    await waitFor(() => {
      expect(screen.getByText('Party not found')).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - PARTY_TYPE_PERSON', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRetrieveParty.mockResolvedValue({
      party: {
        partyId: 'indv-001',
        legalName: 'Jane Smith',
        partyType: 'PARTY_TYPE_PERSON',
        status: 'PARTY_STATUS_ACTIVE',
      },
    })
  })

  it('renders party legal name', async () => {
    renderPartyHeader('indv-001')
    await waitFor(() => {
      expect(screen.getByText('Jane Smith')).toBeInTheDocument()
    })
  })

  it('renders party type label', async () => {
    renderPartyHeader('indv-001')
    await waitFor(() => {
      expect(screen.getByText('PARTY_TYPE_PERSON')).toBeInTheDocument()
    })
  })

  it('renders status badge', async () => {
    renderPartyHeader('indv-001')
    await waitFor(() => {
      expect(screen.getByText(/ACTIVE/)).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - PARTY_TYPE_ORGANIZATION', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRetrieveParty.mockResolvedValue({
      party: {
        partyId: 'org-001',
        legalName: 'Acme Corp',
        partyType: 'PARTY_TYPE_ORGANIZATION',
        status: 'PARTY_STATUS_RESTRICTED',
      },
    })
  })

  it('renders party legal name', async () => {
    renderPartyHeader('org-001')
    await waitFor(() => {
      expect(screen.getByText('Acme Corp')).toBeInTheDocument()
    })
  })

  it('renders party type label', async () => {
    renderPartyHeader('org-001')
    await waitFor(() => {
      expect(screen.getByText('PARTY_TYPE_ORGANIZATION')).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - party name display', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders party name as heading level 2', async () => {
    mockRetrieveParty.mockResolvedValue({
      party: {
        partyId: 'party-h2',
        legalName: 'Test Corporation',
        partyType: 'PARTY_TYPE_ORGANIZATION',
        status: 'PARTY_STATUS_ACTIVE',
      },
    })
    renderPartyHeader('party-h2')
    await waitFor(() => {
      const heading = screen.getByRole('heading', { level: 2 })
      expect(heading).toHaveTextContent('Test Corporation')
    })
  })

  it('calls retrieveParty with the correct partyId', async () => {
    mockRetrieveParty.mockResolvedValue({
      party: {
        partyId: 'specific-id',
        legalName: 'Specific Party',
        partyType: 'PARTY_TYPE_PERSON',
        status: 'PARTY_STATUS_ACTIVE',
      },
    })
    renderPartyHeader('specific-id')
    await waitFor(() => {
      expect(mockRetrieveParty).toHaveBeenCalledWith({ partyId: 'specific-id' })
    })
  })
})
