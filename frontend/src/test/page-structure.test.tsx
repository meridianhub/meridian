/**
 * Structural conformance tests for feature pages.
 *
 * These tests verify that all feature pages adhere to the established UI patterns:
 * - List pages: PageShell wrapper (div.space-y-6) + PageHeader with h1 (text-3xl font-bold tracking-tight)
 * - Detail pages: Breadcrumbs nav + loading skeleton + error state with retry
 *
 * Keeping these tests passing prevents structural drift as new pages are added.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { TooltipProvider } from '@/components/ui/tooltip'
import { server } from './msw-handlers'
import { createTenantUserToken } from './jwt-helpers'
import { renderWithProviders } from './test-utils'

// ---------------------------------------------------------------------------
// vi.mock calls are hoisted to the top of the file by vitest
// ---------------------------------------------------------------------------

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
  useCurrentTenant: () => null,
  useIsPlatformAdmin: () => false,
  useSwitchTenant: () => vi.fn(),
  useClearTenant: () => vi.fn(),
}))

vi.mock('@/contexts/auth-context', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return {
    ...actual,
    useAuth: vi.fn(() => ({ accessToken: 'test-token', logout: vi.fn() })),
    AuthProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  }
})

vi.mock('@/contexts/tenant-context', () => ({
  useTenantContext: vi.fn(() => ({
    tenantSlug: 'test-tenant',
    isPlatformAdmin: false,
    currentTenant: null,
    switchTenant: vi.fn(),
  })),
  TenantProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/api/context', async () => {
  const actual = await vi.importActual('@/api/context')
  return {
    ...actual,
    useClients: vi.fn(() => ({
      party: {
        listParties: vi.fn().mockResolvedValue({ parties: [], nextPageToken: '', totalCount: 0n }),
        listPartyTypes: vi.fn().mockResolvedValue({ partyTypeDefinitions: [] }),
        retrieveParty: vi.fn().mockResolvedValue({
          party: {
            partyId: 'p-001',
            legalName: 'Test Party',
            partyType: 'PARTY_TYPE_ORGANIZATION',
            status: 'PARTY_STATUS_ACTIVE',
          },
        }),
        listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
        retrieveReference: vi.fn().mockResolvedValue({}),
        retrieveAssociations: vi.fn().mockResolvedValue({}),
        retrieveBankRelations: vi.fn().mockResolvedValue({}),
        retrieveDemographics: vi.fn().mockResolvedValue(null),
      },
    })),
    useApiClients: vi.fn(() => ({
      party: {
        listParties: vi.fn().mockResolvedValue({ parties: [], nextPageToken: '', totalCount: 0n }),
        listPartyTypes: vi.fn().mockResolvedValue({ partyTypeDefinitions: [] }),
        retrieveParty: vi.fn().mockResolvedValue({
          party: {
            partyId: 'p-001',
            legalName: 'Test Party',
            partyType: 'PARTY_TYPE_ORGANIZATION',
            status: 'PARTY_STATUS_ACTIVE',
          },
        }),
        listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
        retrieveReference: vi.fn().mockResolvedValue({}),
        retrieveAssociations: vi.fn().mockResolvedValue({}),
        retrieveBankRelations: vi.fn().mockResolvedValue({}),
        retrieveDemographics: vi.fn().mockResolvedValue(null),
      },
    })),
    ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  }
})

// PartyDetailPage uses usePartyDetail from hooks; mock it for controlled state
vi.mock('@/features/parties/hooks', () => ({
  usePartiesTable: vi.fn(() => ({
    queryKey: ['test-tenant', 'parties'],
    queryFn: vi.fn().mockResolvedValue({ items: [], nextPageToken: '' }),
    tenantSlug: 'test-tenant',
  })),
  usePartyDetail: vi.fn(() => ({
    data: {
      partyId: 'p-001',
      legalName: 'Test Party',
      partyType: 'PARTY_TYPE_ORGANIZATION',
      status: 'PARTY_STATUS_ACTIVE',
    },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  })),
  usePartyAssociations: vi.fn(() => ({ data: undefined, isLoading: false, isError: false })),
}))

// PaymentsPage and PaymentDetailPage use these hooks rather than calling MSW directly
vi.mock('@/features/payments/hooks', () => ({
  usePaymentsTable: vi.fn(() => ({
    queryKey: ['test-tenant', 'payments'],
    queryFn: vi.fn().mockResolvedValue({ items: [] }),
    tenantSlug: 'test-tenant',
  })),
  usePaymentDetail: vi.fn(() => ({
    data: undefined,
    isLoading: true,
    isError: false,
    refetch: vi.fn(),
  })),
}))

// Payment dialog mutations require transport — stub them out
vi.mock('@/features/payments/pages/dialogs/payment-mutations', () => ({
  useInitiatePayment: () => ({ mutateAsync: vi.fn(), isPending: false, reset: vi.fn() }),
  useCancelPayment: () => ({ mutateAsync: vi.fn(), isPending: false, reset: vi.fn() }),
  useReversePayment: () => ({ mutateAsync: vi.fn(), isPending: false, reset: vi.fn() }),
}))

// ---------------------------------------------------------------------------
// Static imports (must come after vi.mock declarations)
// ---------------------------------------------------------------------------

import { AccountsPage } from '@/features/accounts/pages'
import { AccountDetailPage } from '@/features/accounts/pages/[accountId]'
import { PartiesPage } from '@/features/parties/pages'
import { PartyDetailPage } from '@/features/parties/pages/[partyId]'
import { PaymentsPage } from '@/features/payments/pages'
import { PaymentDetailPage } from '@/features/payments/pages/payment-detail'
import { usePaymentDetail } from '@/features/payments/hooks'
import { usePartyDetail } from '@/features/parties/hooks'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const tenantToken = createTenantUserToken('tenant-test')

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

/**
 * Render a component at a specific URL path.
 * Uses the full providers from renderWithProviders (AuthProvider, TenantProvider, ApiClientProvider).
 */
function renderAtPath(
  ui: React.ReactElement,
  { path, route }: { path: string; route: string },
) {
  return renderWithProviders(
    <MemoryRouter initialEntries={[route]}>
      <Routes>
        <Route path={path} element={ui} />
      </Routes>
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

/**
 * Render at a path with a full provider stack including TooltipProvider.
 */
function renderAtPathWithTooltip(
  ui: React.ReactElement,
  { path, route }: { path: string; route: string },
) {
  const qc = makeQueryClient()
  return render(
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <MemoryRouter initialEntries={[route]}>
          <Routes>
            <Route path={path} element={ui} />
          </Routes>
        </MemoryRouter>
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

/**
 * Render at the root path (no URL params needed).
 */
function renderAtRoot(ui: React.ReactElement) {
  const qc = makeQueryClient()
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        {ui}
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('Page Structure Conformance', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Re-install default mocks after clearAllMocks
    vi.mocked(usePaymentDetail).mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      refetch: vi.fn(),
    } as ReturnType<typeof usePaymentDetail>)

    vi.mocked(usePartyDetail).mockReturnValue({
      data: {
        partyId: 'p-001',
        legalName: 'Test Party',
        partyType: 'PARTY_TYPE_ORGANIZATION',
        status: 'PARTY_STATUS_ACTIVE',
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    } as ReturnType<typeof usePartyDetail>)
  })

  // -------------------------------------------------------------------------
  // List pages: PageShell wrapper
  // -------------------------------------------------------------------------

  describe('List Pages — PageShell wrapper (div.space-y-6)', () => {
    it('AccountsPage has PageShell wrapper', () => {
      server.use(
        http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
          HttpResponse.json({ accounts: [], nextPageToken: '' }),
        ),
      )

      const { container } = renderAtPath(<AccountsPage />, {
        path: '/accounts',
        route: '/accounts',
      })
      expect(container.querySelector('.space-y-6')).toBeInTheDocument()
    })

    it('PartiesPage has PageShell wrapper', () => {
      const { container } = renderAtRoot(<PartiesPage />)
      expect(container.querySelector('.space-y-6')).toBeInTheDocument()
    })

    it('PaymentsPage has PageShell wrapper', () => {
      const { container } = renderAtRoot(<PaymentsPage />)
      expect(container.querySelector('.space-y-6')).toBeInTheDocument()
    })
  })

  // -------------------------------------------------------------------------
  // List pages: PageHeader h1 with canonical styling
  // -------------------------------------------------------------------------

  describe('List Pages — PageHeader h1 with canonical styling', () => {
    it('AccountsPage has h1 with text-3xl font-bold tracking-tight', () => {
      server.use(
        http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
          HttpResponse.json({ accounts: [], nextPageToken: '' }),
        ),
      )

      const { container } = renderAtPath(<AccountsPage />, {
        path: '/accounts',
        route: '/accounts',
      })
      const h1 = container.querySelector('h1')
      expect(h1).toBeInTheDocument()
      expect(h1).toHaveClass('text-3xl', 'font-bold', 'tracking-tight')
    })

    it('PartiesPage has h1 with text-3xl font-bold tracking-tight', () => {
      const { container } = renderAtRoot(<PartiesPage />)
      const h1 = container.querySelector('h1')
      expect(h1).toBeInTheDocument()
      expect(h1).toHaveClass('text-3xl', 'font-bold', 'tracking-tight')
    })

    it('PaymentsPage has h1 with text-3xl font-bold tracking-tight', () => {
      const { container } = renderAtRoot(<PaymentsPage />)
      const h1 = container.querySelector('h1')
      expect(h1).toBeInTheDocument()
      expect(h1).toHaveClass('text-3xl', 'font-bold', 'tracking-tight')
    })
  })

  // -------------------------------------------------------------------------
  // Detail pages: Breadcrumbs nav element
  // -------------------------------------------------------------------------

  describe('Detail Pages — Breadcrumbs nav element', () => {
    it('AccountDetailPage has Breadcrumbs nav when data loaded', async () => {
      server.use(
        http.post(
          '*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount',
          () =>
            HttpResponse.json({
              facility: {
                accountId: 'acct-001',
                externalIdentifier: 'GB29NWBK60161331926819',
                accountStatus: 1,
                instrumentCode: 'GBP',
                createdAt: '2023-11-14T22:13:20Z',
              },
            }),
        ),
      )

      const { container } = renderAtPath(<AccountDetailPage />, {
        path: '/accounts/:accountId',
        route: '/accounts/acct-001',
      })

      // Breadcrumbs renders <nav aria-label="Breadcrumb">
      await waitFor(() => {
        expect(container.querySelector('nav[aria-label="Breadcrumb"]')).toBeInTheDocument()
      })
    })

    it('PartyDetailPage has Breadcrumbs nav', async () => {
      const { container } = renderAtPath(<PartyDetailPage />, {
        path: '/parties/:partyId',
        route: '/parties/p-001',
      })

      await waitFor(() => {
        expect(container.querySelector('nav[aria-label="Breadcrumb"]')).toBeInTheDocument()
      })
    })

    it('PaymentDetailPage has Breadcrumbs nav when data loaded', async () => {
      vi.mocked(usePaymentDetail).mockReturnValue({
        data: {
          paymentOrderId: 'po-001',
          debtorAccountId: 'acct-debtor-1',
          creditorReference: 'GB29 NWBK 6016 1331 9268 19',
          amount: '10050',
          currency: 'GBP',
          status: 'COMPLETED',
          reference: 'REF-001',
          createdAt: { seconds: BigInt(1700000000), nanos: 0 },
          sagaSteps: [],
          compensationSteps: [],
        },
        isLoading: false,
        isError: false,
        refetch: vi.fn(),
      } as ReturnType<typeof usePaymentDetail>)

      const { container } = renderAtPathWithTooltip(<PaymentDetailPage />, {
        path: '/payments/:paymentOrderId',
        route: '/payments/po-001',
      })

      await waitFor(() => {
        expect(container.querySelector('nav[aria-label="Breadcrumb"]')).toBeInTheDocument()
      })
    })
  })

  // -------------------------------------------------------------------------
  // Detail pages: loading state shows skeleton
  // -------------------------------------------------------------------------

  describe('Detail Pages — loading state shows skeleton', () => {
    it('AccountDetailPage shows DetailSkeleton (data-testid="detail-skeleton") while loading', () => {
      // Never-resolving handler keeps the component in loading state
      server.use(
        http.post(
          '*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount',
          () => new Promise(() => {}),
        ),
      )

      renderAtPath(<AccountDetailPage />, {
        path: '/accounts/:accountId',
        route: '/accounts/acct-001',
      })

      expect(screen.getByTestId('detail-skeleton')).toBeInTheDocument()
    })

    it('PartyDetailPage shows DetailSkeleton (data-testid="detail-skeleton") while loading', () => {
      vi.mocked(usePartyDetail).mockReturnValue({
        data: undefined,
        isLoading: true,
        isError: false,
        refetch: vi.fn(),
      } as ReturnType<typeof usePartyDetail>)

      renderAtPath(<PartyDetailPage />, {
        path: '/parties/:partyId',
        route: '/parties/p-001',
      })

      expect(screen.getByTestId('detail-skeleton')).toBeInTheDocument()
    })

    it('PaymentDetailPage shows loading skeleton (data-testid="payment-detail-skeleton") while loading', () => {
      vi.mocked(usePaymentDetail).mockReturnValue({
        data: undefined,
        isLoading: true,
        isError: false,
        refetch: vi.fn(),
      } as ReturnType<typeof usePaymentDetail>)

      const qc = makeQueryClient()
      render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/payments/po-001']}>
            <Routes>
              <Route path="/payments/:paymentOrderId" element={<PaymentDetailPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>,
      )

      expect(screen.getByTestId('payment-detail-skeleton')).toBeInTheDocument()
    })
  })

  // -------------------------------------------------------------------------
  // Detail pages: error state
  // -------------------------------------------------------------------------

  describe('Detail Pages — error state', () => {
    it('AccountDetailPage shows error state (data-testid="account-error") on fetch failure', async () => {
      server.use(
        http.post(
          '*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount',
          () => HttpResponse.json({ message: 'not found' }, { status: 404 }),
        ),
      )

      renderAtPath(<AccountDetailPage />, {
        path: '/accounts/:accountId',
        route: '/accounts/nonexistent',
      })

      await waitFor(() => {
        expect(screen.getByTestId('account-error')).toBeInTheDocument()
      })
    })

    it('PaymentDetailPage shows error state (data-testid="payment-detail-error") on fetch failure', () => {
      vi.mocked(usePaymentDetail).mockReturnValue({
        data: undefined,
        isLoading: false,
        isError: true,
        refetch: vi.fn(),
      } as ReturnType<typeof usePaymentDetail>)

      const qc = makeQueryClient()
      render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/payments/po-err']}>
            <Routes>
              <Route path="/payments/:paymentOrderId" element={<PaymentDetailPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>,
      )

      expect(screen.getByTestId('payment-detail-error')).toBeInTheDocument()
    })

    it('PartyDetailPage shows ErrorState with retry button on fetch failure', async () => {
      vi.mocked(usePartyDetail).mockReturnValue({
        data: undefined,
        isLoading: false,
        isError: true,
        refetch: vi.fn(),
      } as ReturnType<typeof usePartyDetail>)

      renderAtPath(<PartyDetailPage />, {
        path: '/parties/:partyId',
        route: '/parties/bad-id',
      })

      // ErrorState renders a Retry button when onRetry is provided
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
      })
    })
  })
})
