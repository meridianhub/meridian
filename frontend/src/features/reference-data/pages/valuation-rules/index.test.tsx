import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { ValuationRulesPage } from './index'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

const mockManifestVersion = {
  id: 'mv-1',
  version: '2.0',
  manifest: {
    version: '2.0',
    metadata: { name: 'Test Economy' },
    instruments: [],
    accountTypes: [],
    valuationRules: [
      { fromInstrument: 'KWH', toInstrument: 'GBP', method: 1, source: 'nordpool_spot' },
      { fromInstrument: 'USD', toInstrument: 'EUR', method: 2, source: 'admin_override' },
    ],
    sagas: [],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
  },
}

function mockApiClients(overrides: Record<string, unknown> = {}) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({ version: mockManifestVersion }),
      ...overrides,
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <ValuationRulesPage />
    </MemoryRouter>,
  )
}

describe('ValuationRulesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders breadcrumbs with Economy and Reference Data links', async () => {
    mockApiClients()
    renderPage()

    const breadcrumb = await waitFor(() => screen.getByRole('navigation', { name: 'Breadcrumb' }))
    expect(breadcrumb).toBeInTheDocument()

    const economyLink = screen.getByText('Economy').closest('a')
    expect(economyLink).toHaveAttribute('href', '/economy')

    const refDataLink = screen.getByText('Reference Data').closest('a')
    expect(refDataLink).toHaveAttribute('href', '/reference-data')
  })

  it('renders valuation rules in a table', async () => {
    mockApiClients()
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('KWH')).toBeInTheDocument()
    })

    expect(screen.getByText('GBP')).toBeInTheDocument()
    expect(screen.getByText('USD')).toBeInTheDocument()
    expect(screen.getByText('EUR')).toBeInTheDocument()
    expect(screen.getByText('nordpool_spot')).toBeInTheDocument()
    expect(screen.getByText('admin_override')).toBeInTheDocument()
    expect(screen.getByText('Spot Rate')).toBeInTheDocument()
    expect(screen.getByText('Fixed')).toBeInTheDocument()
  })

  it('renders loading skeleton', () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockReturnValue(new Promise(() => {})),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()
    const skeletons = document.querySelectorAll('.animate-pulse')
    expect(skeletons.length).toBeGreaterThan(0)
  })

  it('renders empty state when no valuation rules exist', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockResolvedValue({
          version: {
            ...mockManifestVersion,
            manifest: { ...mockManifestVersion.manifest, valuationRules: [] },
          },
        }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })

    expect(screen.getByText('No valuation rules')).toBeInTheDocument()
  })

  it('renders error state on fetch failure', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockRejectedValue(new Error('Network error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()

    await waitFor(() => {
      expect(screen.getByText('Failed to load valuation rules.')).toBeInTheDocument()
    })
  })

  it('renders rule name column as from → to', async () => {
    mockApiClients()
    renderPage()

    await waitFor(() => {
      expect(screen.getByText(/KWH → GBP/)).toBeInTheDocument()
      expect(screen.getByText(/USD → EUR/)).toBeInTheDocument()
    })
  })
})
