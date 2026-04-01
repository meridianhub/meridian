import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
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
      { name: 'process_payment', trigger: 'event:payment.requested', script: 'def main(): pass' },
      { name: 'settle_energy', trigger: 'scheduled:daily', script: 'def main(): pass' },
      { name: 'on_meter_read', trigger: 'event:meter.reading', script: 'def main(): pass' },
    ],
    mappings: [
      { name: 'stripe_webhook', targetService: 'meridian.payment_order.v1.PaymentOrderService', targetRpc: 'InitiatePaymentOrder' },
      { name: 'meter_reading_mapping', targetService: 'meridian.energy.v1.EnergyService', targetRpc: 'RecordMeterReading' },
    ],
    seedData: undefined,
    paymentRails: [
      { provider: 'stripe_connect', mode: 1, accountId: 'acct_123', webhookEndpointSecret: 'sm://stripe/webhook' },
    ],
    partyTypes: [
      { id: 'pt-1', tenantId: 't-1', partyType: 'PERSON', attributeSchema: '{}' },
    ],
    operationalGateway: {
      providerConnections: [
        {
          connectionId: 'stripe-payments',
          providerName: 'Stripe',
          providerType: 'payment_gateway',
          protocol: 1,
          baseUrl: 'https://api.stripe.com',
          retryPolicy: { maxAttempts: 3, initialBackoffSeconds: 1, maxBackoffSeconds: 30, backoffMultiplier: 2 },
        },
      ],
      instructionRoutes: [
        {
          instructionType: 'payment.initiate',
          connectionId: 'stripe-payments',
          fallbackConnectionId: '',
          outboundMappingId: '',
          inboundMappingId: '',
          httpMethod: 'POST',
          pathTemplate: '/v1/payments',
        },
      ],
      inboundRoutes: [],
    },
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
    expect(screen.getByText('No custom economy configured')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View Sagas' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View Account Types' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View Valuation Rules' })).toBeInTheDocument()
  })

  it('renders error state when API fails with no cached data', async () => {
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

    it('shows event channels derived from event: saga triggers', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByText('payment.requested')).toBeInTheDocument()
      })
      expect(screen.getByText('meter.reading')).toBeInTheDocument()
    })

    it('shows saga count badge for each channel', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByText('payment.requested')).toBeInTheDocument()
      })
      const sagaBadges = screen.getAllByText(/saga attached/i)
      expect(sagaBadges.length).toBeGreaterThanOrEqual(1)
    })

    it('does not show non-event-triggered sagas as channels', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByText('payment.requested')).toBeInTheDocument()
      })
      // settle_energy has a scheduled: trigger, not event: — should not appear as a channel
      expect(screen.queryByText('daily')).not.toBeInTheDocument()
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

    it('shows mappings after clicking API Endpoints tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /api endpoints/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /api endpoints/i }))

      await waitFor(() => {
        expect(screen.getByText('stripe_webhook')).toBeInTheDocument()
      })
      expect(screen.getByText('meter_reading_mapping')).toBeInTheDocument()
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

  describe('Gateway tab', () => {
    it('renders Gateway tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /gateway/i })).toBeInTheDocument()
      })
    })

    it('shows provider connections after clicking Gateway tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /gateway/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /gateway/i }))

      await waitFor(() => {
        expect(screen.getByTestId('gateway-panel')).toBeInTheDocument()
      })
      expect(screen.getByText('Stripe')).toBeInTheDocument()
      expect(screen.getAllByText('stripe-payments').length).toBeGreaterThanOrEqual(1)
    })

    it('shows instruction routes after clicking Gateway tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /gateway/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /gateway/i }))

      await waitFor(() => {
        expect(screen.getByText('payment.initiate')).toBeInTheDocument()
      })
    })

    it('shows empty state when no gateway configured', async () => {
      const noGatewayManifest = {
        ...mockManifestVersion,
        manifest: {
          ...mockManifestVersion.manifest,
          operationalGateway: undefined,
        },
      }
      mockApiClients({
        getCurrentManifest: vi.fn().mockResolvedValue({ version: noGatewayManifest }),
      })

      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /gateway/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /gateway/i }))

      await waitFor(() => {
        expect(screen.getByTestId('gateway-empty')).toBeInTheDocument()
      })
    })
  })

  describe('Config tab', () => {
    it('renders Config tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /config/i })).toBeInTheDocument()
      })
    })

    it('shows manifest version and applied by after clicking Config tab', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /config/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /config/i }))

      await waitFor(() => {
        expect(screen.getByTestId('config-panel')).toBeInTheDocument()
      })
      expect(screen.getByText('admin@example.com')).toBeInTheDocument()
      expect(screen.getByText('Applied')).toBeInTheDocument()
    })

    it('shows raw manifest JSON', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /config/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /config/i }))

      await waitFor(() => {
        expect(screen.getByTestId('config-raw-manifest')).toBeInTheDocument()
      })
    })

    it('shows download button', async () => {
      renderPage()
      await waitFor(() => {
        expect(screen.getByRole('tab', { name: /config/i })).toBeInTheDocument()
      })
      await userEvent.click(screen.getByRole('tab', { name: /config/i }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /download/i })).toBeInTheDocument()
      })
    })
  })

  it('renders breadcrumbs with link back to /economy', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByLabelText('Breadcrumb')).toBeInTheDocument()
    })

    const breadcrumb = screen.getByLabelText('Breadcrumb')
    const economyLink = within(breadcrumb).getByText('Economy')
    expect(economyLink.closest('a')).toHaveAttribute('href', '/economy')
  })
})
