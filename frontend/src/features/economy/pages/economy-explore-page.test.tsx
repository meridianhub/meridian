import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { EconomyExplorePage } from './economy-explore-page'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

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
      { code: 'CURRENT', name: 'Current Account', normalBalance: 1, allowedInstruments: ['GBP'] },
    ],
    valuationRules: [
      { fromInstrument: 'KWH', toInstrument: 'GBP', cel: 'price * 0.1' },
    ],
    sagas: [
      {
        name: 'process_payment',
        trigger: 'event:payment.requested',
        filter: 'amount > 0',
        script: 'def main(): pass',
      },
      {
        name: 'settle_energy',
        trigger: 'scheduled:daily',
        script: 'def main(): pass',
      },
      {
        name: 'on_meter_read',
        trigger: 'event:meter.reading',
        script: 'def main(): pass',
      },
    ],
    handlers: [
      { name: 'charge_customer', module: 'payments', path: '/v1/payments/charge' },
      { name: 'create_lien', module: 'payments', path: '/v1/payments/lien' },
      { name: 'settle_trade', module: 'trading', path: '/v1/trades/settle' },
    ],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
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

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <EconomyExplorePage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('EconomyExplorePage', () => {
  beforeEach(() => {
    mockApiClients()
  })

  it('renders page title', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('Economy Explorer')).toBeInTheDocument()
    })
  })

  it('renders loading state while fetching', () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockReturnValue(new Promise(() => {})),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()
    expect(screen.getByTestId('explorer-loading')).toBeInTheDocument()
  })

  it('renders empty state when no manifest', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockResolvedValue({ version: undefined }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('explorer-empty')).toBeInTheDocument()
    })
  })

  it('renders error state when API fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockRejectedValue(new Error('Network error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('explorer-error')).toBeInTheDocument()
    })
  })

  describe('Event Channels tab', () => {
    it('renders Event Channels tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /event channels/i })).toBeInTheDocument()
      })
    })

    it('shows bound events with saga badge', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByText('payment.requested')).toBeInTheDocument()
      })
      expect(screen.getByText('meter.reading')).toBeInTheDocument()
      // Both bound events should have saga attached badge
      const sagaBadges = screen.getAllByText(/saga attached/i)
      expect(sagaBadges.length).toBeGreaterThanOrEqual(1)
    })

    it('does not show Add Saga button (channels are derived from saga event: triggers, all are bound)', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByText('payment.requested')).toBeInTheDocument()
      })
      expect(screen.queryByRole('button', { name: /add saga/i })).not.toBeInTheDocument()
    })
  })

  describe('Sagas tab', () => {
    it('renders Sagas tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /^sagas$/i })).toBeInTheDocument()
      })
    })

    it('shows all sagas after clicking Sagas tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /^sagas$/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /^sagas$/i }))

      await waitFor(() => {
        expect(screen.getByText('process_payment')).toBeInTheDocument()
      })
      expect(screen.getByText('settle_energy')).toBeInTheDocument()
      expect(screen.getByText('on_meter_read')).toBeInTheDocument()
    })

    it('shows trigger info for each saga', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /^sagas$/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /^sagas$/i }))

      await waitFor(() => {
        expect(screen.getByText('event:payment.requested')).toBeInTheDocument()
      })
      expect(screen.getByText('scheduled:daily')).toBeInTheDocument()
    })
  })

  describe('API Endpoints tab', () => {
    it('renders API Endpoints tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /api endpoints/i })).toBeInTheDocument()
      })
    })

    it('shows handlers after clicking API Endpoints tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /api endpoints/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /api endpoints/i }))

      await waitFor(() => {
        expect(screen.getByText('charge_customer')).toBeInTheDocument()
      })
      expect(screen.getByText('create_lien')).toBeInTheDocument()
      expect(screen.getByText('settle_trade')).toBeInTheDocument()
    })
  })

  describe('Resources tab', () => {
    it('renders Resources tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /resources/i })).toBeInTheDocument()
      })
    })

    it('shows instruments and account types after clicking Resources tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /resources/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /resources/i }))

      await waitFor(() => {
        expect(screen.getByText('British Pound')).toBeInTheDocument()
      })
      expect(screen.getByText('Kilowatt Hour')).toBeInTheDocument()
      expect(screen.getByText('Current Account')).toBeInTheDocument()
    })
  })
})
