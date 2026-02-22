/**
 * Tests for TenantsPage - platform admin tenant list view
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createPlatformAdminToken } from '@/test/jwt-helpers'
import { TenantsPage } from './index'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

const mockTenants = [
  {
    tenantId: 'acme_corp',
    displayName: 'ACME Corporation',
    slug: 'acme-corp',
    settlementAsset: 'GBP',
    status: 1, // ACTIVE
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    version: 1,
    subdomain: 'acme-corp',
    partyId: '',
    errorMessage: '',
    deprovisionedAt: undefined,
    metadata: undefined,
  },
  {
    tenantId: 'beta_bank',
    displayName: 'Beta Bank',
    slug: 'beta-bank',
    settlementAsset: 'USD',
    status: 4, // PROVISIONING
    createdAt: { seconds: BigInt(1700001000), nanos: 0 },
    version: 1,
    subdomain: 'beta-bank',
    partyId: '',
    errorMessage: '',
    deprovisionedAt: undefined,
    metadata: undefined,
  },
  {
    tenantId: 'gamma_inc',
    displayName: 'Gamma Inc',
    slug: 'gamma-inc',
    settlementAsset: 'EUR',
    status: 2, // SUSPENDED
    createdAt: { seconds: BigInt(1700002000), nanos: 0 },
    version: 2,
    subdomain: 'gamma-inc',
    partyId: '',
    errorMessage: '',
    deprovisionedAt: undefined,
    metadata: undefined,
  },
]

function mockApiClients(overrides: Partial<ReturnType<typeof useApiClients>> = {}) {
  vi.mocked(useApiClients).mockReturnValue({
    tenant: {
      listTenants: vi.fn().mockResolvedValue({ tenants: mockTenants, nextPageToken: '' }),
      initiateTenant: vi.fn().mockResolvedValue({
        tenant: mockTenants[0],
        provisioningHint: 'active',
      }),
      retrieveTenant: vi.fn(),
      updateTenantStatus: vi.fn(),
      getTenantProvisioningStatus: vi.fn(),
      reconcileMigrations: vi.fn(),
    },
    ...overrides,
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderTenantsPage(token: string) {
  return renderWithProviders(
    <MemoryRouter initialEntries={['/tenants']}>
      <Routes>
        <Route path="/tenants" element={<TenantsPage />} />
        <Route path="/tenants/:tenantId" element={<div data-testid="tenant-detail-page">Detail</div>} />
      </Routes>
    </MemoryRouter>,
    { initialToken: token },
  )
}

describe('TenantsPage - access control', () => {
  beforeEach(() => mockApiClients())

  it('renders for platform admin', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /tenant management/i })).toBeInTheDocument()
    })
  })

  it('renders tenant list table for platform admin', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('ACME Corporation')).toBeInTheDocument()
    })
    expect(screen.getByText('Beta Bank')).toBeInTheDocument()
    expect(screen.getByText('Gamma Inc')).toBeInTheDocument()
  })
})

describe('TenantsPage - table columns', () => {
  beforeEach(() => mockApiClients())

  it('shows tenantId column', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('acme_corp')).toBeInTheDocument()
    })
  })

  it('shows displayName column', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('ACME Corporation')).toBeInTheDocument()
    })
  })

  it('shows slug column', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('acme-corp')).toBeInTheDocument()
    })
  })

  it('shows settlementAsset column', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('GBP')).toBeInTheDocument()
    })
  })

  it('shows status as StatusBadge', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getAllByText('ACTIVE').length).toBeGreaterThan(0)
    })
  })

  it('shows createdAt column with TimeDisplay', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      // TimeDisplay renders relative time like "X years ago" or absolute time
      expect(screen.getAllByText(/ago|utc/i).length).toBeGreaterThan(0)
    })
  })
})

describe('TenantsPage - navigation', () => {
  beforeEach(() => mockApiClients())

  it('navigates to tenant detail on row click', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('ACME Corporation')).toBeInTheDocument()
    })

    await userEvent.click(screen.getByText('ACME Corporation'))

    await waitFor(() => {
      expect(screen.getByTestId('tenant-detail-page')).toBeInTheDocument()
    })
  })
})

describe('TenantsPage - empty state', () => {
  it('shows empty state when no tenants', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        listTenants: vi.fn().mockResolvedValue({ tenants: [], nextPageToken: '' }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })
})

describe('TenantsPage - error state', () => {
  it('shows error state when API fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        listTenants: vi.fn().mockRejectedValue(new Error('Internal Server Error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })
  })
})

describe('TenantsPage - InitiateTenant form', () => {
  beforeEach(() => mockApiClients())

  it('renders a button to create a new tenant', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /new tenant/i })).toBeInTheDocument()
    })
  })

  it('opens form dialog when create button is clicked', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /new tenant/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /new tenant/i }))

    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('form validates required fields - tenantId required', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /new tenant/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /new tenant/i }))

    // Try submitting without filling required fields (click Initiate Tenant button in dialog)
    const submitButton = screen.getByRole('button', { name: /^initiate tenant$/i })
    await userEvent.click(submitButton)

    await waitFor(() => {
      expect(screen.getByText(/tenant id is required/i)).toBeInTheDocument()
    })
  })

  it('form validates required fields - displayName required', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /new tenant/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /new tenant/i }))

    const tenantIdInput = screen.getByLabelText(/tenant id/i)
    await userEvent.type(tenantIdInput, 'test_tenant')

    const submitButton = screen.getByRole('button', { name: /^initiate tenant$/i })
    await userEvent.click(submitButton)

    await waitFor(() => {
      expect(screen.getByText(/display name is required/i)).toBeInTheDocument()
    })
  })

  it('form validates required fields - settlementAsset required', async () => {
    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /new tenant/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /new tenant/i }))

    const tenantIdInput = screen.getByLabelText(/tenant id/i)
    const displayNameInput = screen.getByLabelText(/display name/i)
    await userEvent.type(tenantIdInput, 'test_tenant')
    await userEvent.type(displayNameInput, 'Test Tenant')

    const submitButton = screen.getByRole('button', { name: /^initiate tenant$/i })
    await userEvent.click(submitButton)

    await waitFor(() => {
      expect(screen.getByText(/settlement asset is required/i)).toBeInTheDocument()
    })
  })

  it('submits form with valid data', async () => {
    const initiateMock = vi.fn().mockResolvedValue({
      tenant: mockTenants[0],
      provisioningHint: 'active',
    })

    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        listTenants: vi.fn().mockResolvedValue({ tenants: mockTenants, nextPageToken: '' }),
        initiateTenant: initiateMock,
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantsPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /new tenant/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /new tenant/i }))

    const tenantIdInput = screen.getByLabelText(/tenant id/i)
    const displayNameInput = screen.getByLabelText(/display name/i)
    const settlementAssetInput = screen.getByLabelText(/settlement asset/i)

    await userEvent.type(tenantIdInput, 'new_tenant')
    await userEvent.type(displayNameInput, 'New Tenant')
    await userEvent.type(settlementAssetInput, 'GBP')

    const submitButton = screen.getByRole('button', { name: /^initiate tenant$/i })
    await userEvent.click(submitButton)

    await waitFor(() => {
      expect(initiateMock).toHaveBeenCalledWith(
        expect.objectContaining({
          tenantId: 'new_tenant',
          displayName: 'New Tenant',
          settlementAsset: 'GBP',
        }),
      )
    })
  })
})
