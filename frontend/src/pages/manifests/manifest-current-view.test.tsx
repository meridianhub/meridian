import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ManifestCurrentView } from './manifest-current-view'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

const mockManifestVersion = {
  id: 'mv-123',
  version: '1.0',
  manifest: {
    version: '1.0',
    metadata: {
      name: 'Acme Energy',
      industry: 'energy',
      description: 'Test manifest',
    },
    instruments: [
      {
        code: 'GBP',
        name: 'British Pound',
        type: 1,
        dimensions: { unit: 'GBP', precision: 2 },
      },
      {
        code: 'KWH',
        name: 'Kilowatt Hour',
        type: 2,
        dimensions: { unit: 'kWh', precision: 4 },
      },
    ],
    accountTypes: [
      {
        code: 'CURRENT',
        name: 'Current Account',
        normalBalance: 1,
        allowedInstruments: [],
        policies: undefined,
      },
    ],
    valuationRules: [],
    sagas: [
      {
        name: 'process_payment',
        trigger: 'api:/v1/payments',
        script: 'def main(): pass',
      },
    ],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
  },
  appliedAt: { seconds: BigInt(1700000000), nanos: 0 },
  appliedBy: 'admin@example.com',
  applyStatus: ApplyStatus.APPLIED,
  applyJobId: 'job-456',
  diffSummary: 'Added 2 instruments',
  createdAt: { seconds: BigInt(1700000000), nanos: 0 },
}

function mockApiClients(overrides: Record<string, unknown> = {}) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({ version: mockManifestVersion }),
      listManifestVersions: vi.fn(),
      getManifestVersion: vi.fn(),
      ...overrides,
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderComponent() {
  return renderWithProviders(
    <MemoryRouter>
      <ManifestCurrentView />
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('ManifestCurrentView', () => {
  beforeEach(() => mockApiClients())

  it('renders loading skeleton while query pending', () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockReturnValue(new Promise(() => {})),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderComponent()

    expect(screen.getByTestId('loading-skeleton')).toBeInTheDocument()
  })

  it('renders current manifest version and status', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('Version 1.0')).toBeInTheDocument()
    })
    expect(screen.getByText('APPLIED')).toBeInTheDocument()
  })

  it('renders appliedBy', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText(/admin@example.com/)).toBeInTheDocument()
    })
  })

  it('renders manifest metadata', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('Acme Energy')).toBeInTheDocument()
    })
    expect(screen.getByText('(energy)')).toBeInTheDocument()
  })

  it('renders expandable sections with counts', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText(/Instruments \(2\)/)).toBeInTheDocument()
    })
    expect(screen.getByText(/Account Types \(1\)/)).toBeInTheDocument()
    expect(screen.getByText(/Valuation Rules \(0\)/)).toBeInTheDocument()
    expect(screen.getByText(/Sagas \(1\)/)).toBeInTheDocument()
  })

  it('expands instruments section on click', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText(/Instruments \(2\)/)).toBeInTheDocument()
    })

    await userEvent.click(screen.getByText(/Instruments \(2\)/))

    expect(screen.getByText('GBP')).toBeInTheDocument()
    expect(screen.getByText('British Pound')).toBeInTheDocument()
    expect(screen.getByText('KWH')).toBeInTheDocument()
  })

  it('shows empty state when no manifest applied', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockResolvedValue({ version: undefined }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderComponent()

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('shows error state with retry button when API fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockRejectedValue(new Error('Server Error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderComponent()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })
  })
})
