import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConnectError, Code } from '@connectrpc/connect'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { EconomyOverviewPage } from './economy-overview-page'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

// ManifestGraph and ManifestHistoryTable are complex components — stub them for unit tests
vi.mock('@/features/manifests/components/manifest-graph', () => ({
  ManifestGraph: () => <div data-testid="manifest-graph" />,
}))

vi.mock('@/features/manifests/pages/manifest-history-table', () => ({
  ManifestHistoryTable: () => <div data-testid="manifest-history-table" />,
}))

import { useApiClients } from '@/api/context'

const mockManifestVersion = {
  id: 'mv-1',
  version: '2.0',
  manifest: {
    version: '2.0',
    metadata: {
      name: 'Acme Energy',
      industry: 'energy',
      description: 'An energy economy for testing',
    },
    instruments: [
      { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
      { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
    ],
    accountTypes: [
      { code: 'CURRENT', name: 'Current Account', normalBalance: 1, allowedInstruments: [] },
    ],
    valuationRules: [
      { fromInstrument: 'KWH', toInstrument: 'GBP', cel: 'price * 0.1' },
    ],
    sagas: [
      { name: 'process_payment', trigger: 'api:/v1/payments', script: 'def main(): pass' },
      { name: 'settle_energy', trigger: 'scheduled:daily', script: 'def main(): pass' },
    ],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
    handlers: [
      { name: 'charge_customer', module: 'payments' },
      { name: 'create_lien', module: 'payments' },
      { name: 'settle_trade', module: 'trading' },
    ],
  },
  appliedAt: { seconds: BigInt(1700000000), nanos: 0 },
  appliedBy: 'admin@example.com',
  applyStatus: ApplyStatus.APPLIED,
  applyJobId: 'job-1',
  diffSummary: 'Added energy instruments',
}

function mockApiClients(overrides: Record<string, unknown> = {}) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({ version: mockManifestVersion }),
      listManifestVersions: vi.fn().mockResolvedValue({ versions: [], totalCount: 0 }),
      ...overrides,
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return { ...actual, useNavigate: () => mockNavigate }
})

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <EconomyOverviewPage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('EconomyOverviewPage', () => {
  beforeEach(() => {
    mockApiClients()
    mockNavigate.mockClear()
  })

  it('renders loading skeleton while fetching manifest', () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockReturnValue(new Promise(() => {})),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()

    expect(screen.getByTestId('overview-loading')).toBeInTheDocument()
  })

  it('renders economy name from manifest metadata', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('Acme Energy')).toBeInTheDocument()
    })
  })

  it('renders economy description from manifest metadata', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('An energy economy for testing')).toBeInTheDocument()
    })
  })

  it('renders industry tag from manifest metadata', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('energy')).toBeInTheDocument()
    })
  })

  it('renders stats cards with correct counts', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('stat-instruments')).toHaveTextContent('2')
    })
    expect(screen.getByTestId('stat-account-types')).toHaveTextContent('1')
    expect(screen.getByTestId('stat-sagas')).toHaveTextContent('2')
    expect(screen.getByTestId('stat-valuation-rules')).toHaveTextContent('1')
  })

  it('renders Explore button that navigates to /economy/explore', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /explore/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /explore/i }))
    expect(mockNavigate).toHaveBeenCalledWith('/economy/explore')
  })

  it('renders Edit Economy button that navigates to /economy/edit', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /edit economy/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /edit economy/i }))
    expect(mockNavigate).toHaveBeenCalledWith('/economy/edit')
  })

  it('renders ManifestGraph component', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('manifest-graph')).toBeInTheDocument()
    })
  })

  it('renders ManifestHistoryTable component', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('manifest-history-table')).toBeInTheDocument()
    })
  })

  it('shows empty state when no manifest version applied', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockResolvedValue({ version: undefined }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('overview-empty')).toBeInTheDocument()
    })
  })

  it('shows error state when API fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockRejectedValue(new Error('Network error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('overview-error')).toBeInTheDocument()
    })
  })

  it('shows empty state when API returns NotFound', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockRejectedValue(
          new ConnectError('no applied manifest found', Code.NotFound),
        ),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('overview-empty')).toBeInTheDocument()
    })
    expect(screen.getByText('No economy configured')).toBeInTheDocument()
  })

  it('renders compact stat cards inside a stats-bar container', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('stats-bar')).toBeInTheDocument()
    })

    // Stat chips should be inside the stats-bar
    const statsBar = screen.getByTestId('stats-bar')
    expect(statsBar.querySelector('[data-testid="stat-instruments"]')).toBeInTheDocument()
  })

  it('stat cards are clickable buttons', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('stat-instruments')).toBeInTheDocument()
    })

    const instruments = screen.getByTestId('stat-instruments')
    const clickableCard = instruments.closest('button')
    expect(clickableCard).toBeInTheDocument()
  })

  it('renders breadcrumbs navigation', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByLabelText('Breadcrumb')).toBeInTheDocument()
    })
  })

  it('renders breadcrumbs even during loading state', () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockReturnValue(new Promise(() => {})),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()

    expect(screen.getByTestId('overview-loading')).toBeInTheDocument()
    expect(screen.getByLabelText('Breadcrumb')).toBeInTheDocument()
  })
})
