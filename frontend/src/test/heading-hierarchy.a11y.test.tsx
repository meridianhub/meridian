/**
 * Heading hierarchy accessibility tests.
 *
 * Screen reader users rely on heading hierarchy for page navigation. These tests
 * validate that rendered page compositions:
 *   - Have exactly one h1
 *   - Do not skip heading levels (e.g. h1 → h3 without h2)
 *   - Have headings in sequential order
 *
 * If an actual violation is found, the test is skipped with a comment explaining
 * the issue so that it can be tracked and fixed separately.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

// ---------------------------------------------------------------------------
// vi.mock calls must be hoisted to the top of the file
// ---------------------------------------------------------------------------

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: { listPaymentOrders: vi.fn().mockResolvedValue({ paymentOrders: [], pagination: { totalCount: BigInt(0), nextPageToken: '' } }) },
    financialAccounting: {
      listFinancialBookingLogs: vi.fn().mockResolvedValue({ financialBookingLogs: [], pagination: { totalCount: BigInt(0), nextPageToken: '' } }),
      listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [], pagination: { totalCount: BigInt(0), nextPageToken: '' } }),
    },
    positionKeeping: {},
    accountReconciliation: {},
    party: {},
    tenant: {},
    sagaRegistry: {},
    sagaAdmin: {},
    referenceData: {},
    accountTypeRegistry: {},
    node: {},
    internalAccount: {},
    marketInformation: {},
    manifestHistory: {
      getCurrentManifest: vi.fn(),
      listManifestVersions: vi.fn().mockResolvedValue({ versions: [], totalCount: 0 }),
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

vi.mock('@/features/manifests/components/manifest-graph', () => ({
  ManifestGraph: () => <div data-testid="manifest-graph" />,
}))

vi.mock('@/features/manifests/pages/manifest-history-table', () => ({
  ManifestHistoryTable: () => <div data-testid="manifest-history-table" />,
}))

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return { ...actual, useNavigate: () => vi.fn() }
})

// ---------------------------------------------------------------------------
// Static imports (must come after vi.mock declarations)
// ---------------------------------------------------------------------------

import { DashboardPage } from '@/features/dashboard/pages/index'
import { EconomyOverviewPage } from '@/features/economy/pages/economy-overview-page'
import { ReferenceDataHubPage } from '@/features/reference-data/pages/index'
import { InstrumentsPage } from '@/features/reference-data/pages/instruments/index'
import { useApiClients } from '@/api/context'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const tenantToken = createTenantUserToken('test-tenant')

function renderPage(ui: React.ReactElement) {
  return renderWithProviders(
    <MemoryRouter>
      {ui}
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

/**
 * Collects all heading elements in render order and returns their numeric levels.
 */
function getHeadingLevels(container: HTMLElement): number[] {
  const headings = Array.from(container.querySelectorAll('h1, h2, h3, h4, h5, h6'))
  return headings.map((h) => parseInt(h.tagName[1], 10))
}

/**
 * Validates that heading levels form a valid hierarchy:
 * - Exactly one h1
 * - No skipped levels (an increase of more than 1 is invalid)
 */
function validateHeadingHierarchy(levels: number[]): { valid: boolean; violations: string[] } {
  const violations: string[] = []

  const h1Count = levels.filter((l) => l === 1).length
  if (h1Count === 0) {
    violations.push('No h1 found — every page must have exactly one h1')
  } else if (h1Count > 1) {
    violations.push(`${h1Count} h1 elements found — every page must have exactly one h1`)
  }

  for (let i = 1; i < levels.length; i++) {
    const prev = levels[i - 1]
    const curr = levels[i]
    if (curr > prev + 1) {
      violations.push(`Heading level skipped: h${prev} → h${curr} (missing h${prev + 1})`)
    }
  }

  return { valid: violations.length === 0, violations }
}

// ---------------------------------------------------------------------------
// Mock data for economy overview page
// ---------------------------------------------------------------------------

const mockManifestVersion = {
  id: 'mv-1',
  version: '2.0',
  manifest: {
    version: '2.0',
    metadata: { name: 'Acme Energy', industry: 'energy', description: 'Test economy' },
    instruments: [{ code: 'GBP' }, { code: 'KWH' }],
    accountTypes: [{ code: 'CURRENT' }],
    valuationRules: [{ fromInstrument: 'KWH', toInstrument: 'GBP', cel: 'price * 0.1' }],
    sagas: [{ name: 'process_payment' }],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
    handlers: [],
  },
  appliedAt: { seconds: BigInt(1700000000), nanos: 0 },
  appliedBy: 'admin@example.com',
  applyStatus: ApplyStatus.APPLIED,
  applyJobId: 'job-1',
  diffSummary: 'Initial',
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('Heading hierarchy accessibility', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('Dashboard page', () => {
    it('has exactly one h1 and no skipped heading levels', () => {
      const { container } = renderPage(<DashboardPage />)

      const levels = getHeadingLevels(container)
      const { valid, violations } = validateHeadingHierarchy(levels)

      expect(valid, `Heading hierarchy violations: ${violations.join('; ')}`).toBe(true)
    })
  })

  describe('Economy Overview page', () => {
    beforeEach(() => {
      vi.mocked(useApiClients).mockReturnValue({
        manifestHistory: {
          getCurrentManifest: vi.fn().mockResolvedValue({ version: mockManifestVersion }),
          listManifestVersions: vi.fn().mockResolvedValue({ versions: [], totalCount: 0 }),
        },
      } as unknown as ReturnType<typeof useApiClients>)
    })

    it('has exactly one h1 and no skipped heading levels when manifest loaded', async () => {
      const { container } = renderPage(<EconomyOverviewPage />)

      // Wait for manifest data to load so h1 (economy name) is rendered
      await waitFor(() => {
        expect(screen.getByText('Acme Energy')).toBeInTheDocument()
      })

      const levels = getHeadingLevels(container)
      const { valid, violations } = validateHeadingHierarchy(levels)

      expect(valid, `Heading hierarchy violations: ${violations.join('; ')}`).toBe(true)
    })
  })

  describe('Reference Data Hub page', () => {
    beforeEach(() => {
      vi.mocked(useApiClients).mockReturnValue({
        referenceData: {
          listInstruments: vi.fn().mockResolvedValue({ instruments: [] }),
        },
        accountTypeRegistry: {
          listActive: vi.fn().mockResolvedValue({ definitions: [] }),
        },
        node: {
          getChildren: vi.fn().mockResolvedValue({ nodes: [] }),
        },
        manifestHistory: {
          getCurrentManifest: vi.fn().mockResolvedValue({
            version: { manifest: { valuationRules: [] } },
          }),
        },
      } as unknown as ReturnType<typeof useApiClients>)
    })

    it('has exactly one h1 and no skipped heading levels', () => {
      const { container } = renderPage(<ReferenceDataHubPage />)

      const levels = getHeadingLevels(container)
      const { valid, violations } = validateHeadingHierarchy(levels)

      expect(valid, `Heading hierarchy violations: ${violations.join('; ')}`).toBe(true)
    })
  })

  describe('Instruments detail page', () => {
    beforeEach(() => {
      vi.mocked(useApiClients).mockReturnValue({
        referenceData: {
          listInstruments: vi.fn().mockResolvedValue({ instruments: [], nextPageToken: '' }),
          evaluateInstrument: vi.fn().mockResolvedValue({
            compileErrors: [],
            validationResult: true,
            fungibilityKey: '',
            errorMessage: '',
          }),
        },
      } as unknown as ReturnType<typeof useApiClients>)
    })

    it('has exactly one h1 and no skipped heading levels', () => {
      const { container } = renderPage(<InstrumentsPage />)

      const levels = getHeadingLevels(container)
      const { valid, violations } = validateHeadingHierarchy(levels)

      expect(valid, `Heading hierarchy violations: ${violations.join('; ')}`).toBe(true)
    })
  })
})
