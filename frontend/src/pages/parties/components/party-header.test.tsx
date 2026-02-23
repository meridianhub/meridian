import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PartyHeader } from './party-header'

const mockGetParticipant = vi.fn()

vi.mock('@/api/context', () => ({
  useClients: vi.fn(() => ({
    party: {
      getParticipant: mockGetParticipant,
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
    mockGetParticipant.mockReturnValue(new Promise(() => {})) // never resolves
  })

  it('renders skeleton placeholders while loading', () => {
    renderPartyHeader()
    // Skeletons are rendered during loading - check for loading container
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
    mockGetParticipant.mockResolvedValue(undefined)
  })

  it('shows "Party not found" when party data is undefined', async () => {
    renderPartyHeader()

    await waitFor(() => {
      expect(screen.getByText('Party not found')).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - INDIVIDUAL party type', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetParticipant.mockResolvedValue({
      partyId: 'indv-001',
      name: 'Jane Smith',
      partyType: 'INDIVIDUAL',
      status: 'ACTIVE',
    })
  })

  it('renders party name', async () => {
    renderPartyHeader('indv-001')
    await waitFor(() => {
      expect(screen.getByText('Jane Smith')).toBeInTheDocument()
    })
  })

  it('renders INDIVIDUAL party type label', async () => {
    renderPartyHeader('indv-001')
    await waitFor(() => {
      expect(screen.getByText('INDIVIDUAL')).toBeInTheDocument()
    })
  })

  it('renders ACTIVE status badge', async () => {
    renderPartyHeader('indv-001')
    await waitFor(() => {
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - ORGANIZATION party type', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetParticipant.mockResolvedValue({
      partyId: 'org-001',
      name: 'Acme Corp',
      partyType: 'ORGANIZATION',
      status: 'INACTIVE',
    })
  })

  it('renders party name', async () => {
    renderPartyHeader('org-001')
    await waitFor(() => {
      expect(screen.getByText('Acme Corp')).toBeInTheDocument()
    })
  })

  it('renders ORGANIZATION party type label', async () => {
    renderPartyHeader('org-001')
    await waitFor(() => {
      expect(screen.getByText('ORGANIZATION')).toBeInTheDocument()
    })
  })

  it('renders INACTIVE status badge', async () => {
    renderPartyHeader('org-001')
    await waitFor(() => {
      expect(screen.getByText('INACTIVE')).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - GOVERNMENT party type', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetParticipant.mockResolvedValue({
      partyId: 'gov-001',
      name: 'HM Treasury',
      partyType: 'GOVERNMENT',
      status: 'SUSPENDED',
    })
  })

  it('renders party name', async () => {
    renderPartyHeader('gov-001')
    await waitFor(() => {
      expect(screen.getByText('HM Treasury')).toBeInTheDocument()
    })
  })

  it('renders GOVERNMENT party type label', async () => {
    renderPartyHeader('gov-001')
    await waitFor(() => {
      expect(screen.getByText('GOVERNMENT')).toBeInTheDocument()
    })
  })

  it('renders SUSPENDED status badge', async () => {
    renderPartyHeader('gov-001')
    await waitFor(() => {
      expect(screen.getByText('SUSPENDED')).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - status badge variants', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders PENDING_VERIFICATION status', async () => {
    mockGetParticipant.mockResolvedValue({
      partyId: 'party-pv',
      name: 'Pending Party',
      partyType: 'INDIVIDUAL',
      status: 'PENDING_VERIFICATION',
    })
    renderPartyHeader('party-pv')
    await waitFor(() => {
      // StatusBadge renders the status, which may format underscores as spaces
      expect(screen.getByText(/pending.?verification/i)).toBeInTheDocument()
    })
  })
})

describe('PartyHeader - verification status', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows verification status when provided', async () => {
    mockGetParticipant.mockResolvedValue({
      partyId: 'party-v',
      name: 'Verified Party',
      partyType: 'ORGANIZATION',
      status: 'ACTIVE',
      verificationStatus: 'KYC_COMPLETE',
    })
    renderPartyHeader('party-v')
    await waitFor(() => {
      expect(screen.getByText('Verification: KYC_COMPLETE')).toBeInTheDocument()
    })
  })

  it('does not show verification section when verificationStatus is absent', async () => {
    mockGetParticipant.mockResolvedValue({
      partyId: 'party-nv',
      name: 'Unverified Party',
      partyType: 'INDIVIDUAL',
      status: 'ACTIVE',
    })
    renderPartyHeader('party-nv')
    await waitFor(() => {
      expect(screen.getByText('Unverified Party')).toBeInTheDocument()
    })
    expect(screen.queryByText(/verification:/i)).not.toBeInTheDocument()
  })

  it('does not show verification section when verificationStatus is empty string', async () => {
    mockGetParticipant.mockResolvedValue({
      partyId: 'party-ev',
      name: 'Empty Verification',
      partyType: 'INDIVIDUAL',
      status: 'ACTIVE',
      verificationStatus: '',
    })
    renderPartyHeader('party-ev')
    await waitFor(() => {
      expect(screen.getByText('Empty Verification')).toBeInTheDocument()
    })
    expect(screen.queryByText(/verification:/i)).not.toBeInTheDocument()
  })
})

describe('PartyHeader - party name display', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders party name as heading level 2', async () => {
    mockGetParticipant.mockResolvedValue({
      partyId: 'party-h2',
      name: 'Test Corporation',
      partyType: 'ORGANIZATION',
      status: 'ACTIVE',
    })
    renderPartyHeader('party-h2')
    await waitFor(() => {
      const heading = screen.getByRole('heading', { level: 2 })
      expect(heading).toHaveTextContent('Test Corporation')
    })
  })

  it('calls getParticipant with the correct partyId', async () => {
    mockGetParticipant.mockResolvedValue({
      partyId: 'specific-id',
      name: 'Specific Party',
      partyType: 'INDIVIDUAL',
      status: 'ACTIVE',
    })
    renderPartyHeader('specific-id')
    await waitFor(() => {
      expect(mockGetParticipant).toHaveBeenCalledWith({ partyId: 'specific-id' })
    })
  })
})
