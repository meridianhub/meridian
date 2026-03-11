/**
 * Tests for TenantDetailPage - tenant detail view with provisioning status
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createPlatformAdminToken } from '@/test/jwt-helpers'
import { TenantDetailPage } from './[tenantId]'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

const mockActiveTenant = {
  tenantId: 'acme_corp',
  displayName: 'ACME Corporation',
  slug: 'acme-corp',
  settlementAsset: 'GBP',
  status: 1, // ACTIVE = 1
  createdAt: { seconds: BigInt(1700000000), nanos: 0 },
  version: 1,
  subdomain: 'acme-corp',
  partyId: 'party-001',
  errorMessage: '',
  deprovisionedAt: undefined,
  metadata: undefined,
}

const mockProvisioningTenant = {
  ...mockActiveTenant,
  tenantId: 'beta_bank',
  displayName: 'Beta Bank',
  slug: 'beta-bank',
  status: 4, // PROVISIONING = 4
}

const mockProvisioningStatus = {
  tenantId: 'acme_corp',
  overallStatus: 1, // ACTIVE
  services: [
    {
      serviceName: 'ledger',
      status: 3, // COMPLETED
      migrationVersion: '20240101000001',
      errorMessage: '',
      startedAt: { seconds: BigInt(1700000100), nanos: 0 },
      completedAt: { seconds: BigInt(1700000200), nanos: 0 },
    },
    {
      serviceName: 'payment',
      status: 3, // COMPLETED
      migrationVersion: '20240101000002',
      errorMessage: '',
      startedAt: { seconds: BigInt(1700000150), nanos: 0 },
      completedAt: { seconds: BigInt(1700000250), nanos: 0 },
    },
  ],
  errorMessage: '',
}

const mockProvisioningStatusInProgress = {
  tenantId: 'beta_bank',
  overallStatus: 4, // PROVISIONING
  services: [
    {
      serviceName: 'ledger',
      status: 3, // COMPLETED
      migrationVersion: '20240101000001',
      errorMessage: '',
      startedAt: { seconds: BigInt(1700000100), nanos: 0 },
      completedAt: { seconds: BigInt(1700000200), nanos: 0 },
    },
    {
      serviceName: 'payment',
      status: 2, // IN_PROGRESS
      migrationVersion: '',
      errorMessage: '',
      startedAt: { seconds: BigInt(1700000150), nanos: 0 },
      completedAt: undefined,
    },
  ],
  errorMessage: '',
}

function makeTenantApi(tenantOverrides: Record<string, unknown> = {}, provisioningOverrides: Record<string, unknown> = {}) {
  return {
    tenant: {
      retrieveTenant: vi.fn().mockResolvedValue({ tenant: mockActiveTenant, ...tenantOverrides }),
      getTenantProvisioningStatus: vi.fn().mockResolvedValue({ ...mockProvisioningStatus, ...provisioningOverrides }),
      updateTenantStatus: vi.fn().mockResolvedValue({ tenant: mockActiveTenant }),
      listTenants: vi.fn(),
      initiateTenant: vi.fn(),
      reconcileMigrations: vi.fn(),
    },
  }
}

function renderTenantDetailPage(tenantId: string, token: string) {
  return renderWithProviders(
    <MemoryRouter initialEntries={[`/tenants/${tenantId}`]}>
      <Routes>
        <Route path="/tenants/:tenantId" element={<TenantDetailPage />} />
        <Route path="/tenants" element={<div data-testid="tenants-list-page">Tenants List</div>} />
      </Routes>
    </MemoryRouter>,
    { initialToken: token },
  )
}

describe('TenantDetailPage - renders tenant details', () => {
  beforeEach(() => {
    vi.mocked(useApiClients).mockReturnValue(
      makeTenantApi() as unknown as ReturnType<typeof useApiClients>,
    )
  })

  it('shows tenant display name as page heading', async () => {
    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ACME Corporation' })).toBeInTheDocument()
    })
  })

  it('shows tenant ID', async () => {
    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getAllByText('acme_corp').length).toBeGreaterThan(0)
    })
  })

  it('shows settlement asset', async () => {
    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('GBP')).toBeInTheDocument()
    })
  })

  it('shows tenant status badge', async () => {
    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getAllByText('ACTIVE').length).toBeGreaterThan(0)
    })
  })
})

describe('TenantDetailPage - provisioning status grid', () => {
  beforeEach(() => {
    vi.mocked(useApiClients).mockReturnValue(
      makeTenantApi() as unknown as ReturnType<typeof useApiClients>,
    )
  })

  it('renders per-service provisioning status', async () => {
    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('ledger')).toBeInTheDocument()
      expect(screen.getByText('payment')).toBeInTheDocument()
    })
  })

  it('shows migration version for each service', async () => {
    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('20240101000001')).toBeInTheDocument()
      expect(screen.getByText('20240101000002')).toBeInTheDocument()
    })
  })
})

describe('TenantDetailPage - provisioning polling', () => {
  it('fetches provisioning status for provisioning tenant', async () => {
    const getProvisioningStatusMock = vi.fn().mockResolvedValue(mockProvisioningStatusInProgress)

    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        retrieveTenant: vi.fn().mockResolvedValue({ tenant: mockProvisioningTenant }),
        getTenantProvisioningStatus: getProvisioningStatusMock,
        updateTenantStatus: vi.fn(),
        listTenants: vi.fn(),
        initiateTenant: vi.fn(),
        reconcileMigrations: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantDetailPage('beta_bank', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Beta Bank' })).toBeInTheDocument()
    })

    // The component should have called the provisioning status API at least once
    expect(getProvisioningStatusMock).toHaveBeenCalled()
  })

  it('shows both completed and in-progress service statuses', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        retrieveTenant: vi.fn().mockResolvedValue({ tenant: mockProvisioningTenant }),
        getTenantProvisioningStatus: vi.fn().mockResolvedValue(mockProvisioningStatusInProgress),
        updateTenantStatus: vi.fn(),
        listTenants: vi.fn(),
        initiateTenant: vi.fn(),
        reconcileMigrations: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantDetailPage('beta_bank', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('ledger')).toBeInTheDocument()
      expect(screen.getByText('payment')).toBeInTheDocument()
    })
  })
})

describe('TenantDetailPage - lifecycle actions', () => {
  it('shows Suspend button for ACTIVE tenant', async () => {
    vi.mocked(useApiClients).mockReturnValue(
      makeTenantApi() as unknown as ReturnType<typeof useApiClients>,
    )

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /suspend/i })).toBeInTheDocument()
    })
  })

  it('does not show Activate button for ACTIVE tenant', async () => {
    vi.mocked(useApiClients).mockReturnValue(
      makeTenantApi() as unknown as ReturnType<typeof useApiClients>,
    )

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /suspend/i })).toBeInTheDocument()
    })

    expect(screen.queryByRole('button', { name: /^activate$/i })).not.toBeInTheDocument()
  })

  it('shows Activate button for SUSPENDED tenant', async () => {
    const suspendedTenant = { ...mockActiveTenant, status: 2 } // SUSPENDED = 2

    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        retrieveTenant: vi.fn().mockResolvedValue({ tenant: suspendedTenant }),
        getTenantProvisioningStatus: vi.fn().mockResolvedValue({ ...mockProvisioningStatus, overallStatus: 2 }),
        updateTenantStatus: vi.fn().mockResolvedValue({ tenant: { ...suspendedTenant, status: 1 } }),
        listTenants: vi.fn(),
        initiateTenant: vi.fn(),
        reconcileMigrations: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^activate$/i })).toBeInTheDocument()
    })
  })

  it('does not show Suspend button for SUSPENDED tenant', async () => {
    const suspendedTenant = { ...mockActiveTenant, status: 2 } // SUSPENDED = 2

    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        retrieveTenant: vi.fn().mockResolvedValue({ tenant: suspendedTenant }),
        getTenantProvisioningStatus: vi.fn().mockResolvedValue({ ...mockProvisioningStatus, overallStatus: 2 }),
        updateTenantStatus: vi.fn().mockResolvedValue({ tenant: { ...suspendedTenant, status: 1 } }),
        listTenants: vi.fn(),
        initiateTenant: vi.fn(),
        reconcileMigrations: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^activate$/i })).toBeInTheDocument()
    })

    expect(screen.queryByRole('button', { name: /suspend/i })).not.toBeInTheDocument()
  })

  it('calls UpdateTenantStatus when Suspend is clicked', async () => {
    const updateStatusMock = vi.fn().mockResolvedValue({ tenant: { ...mockActiveTenant, status: 2 } })

    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        retrieveTenant: vi.fn().mockResolvedValue({ tenant: mockActiveTenant }),
        getTenantProvisioningStatus: vi.fn().mockResolvedValue(mockProvisioningStatus),
        updateTenantStatus: updateStatusMock,
        listTenants: vi.fn(),
        initiateTenant: vi.fn(),
        reconcileMigrations: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /suspend/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /suspend/i }))

    await waitFor(() => {
      expect(updateStatusMock).toHaveBeenCalledWith(
        expect.objectContaining({ tenantId: 'acme_corp', status: 2 }), // SUSPENDED = 2
      )
    })
  })

  it('calls UpdateTenantStatus when Activate is clicked', async () => {
    const suspendedTenant = { ...mockActiveTenant, status: 2 }
    const updateStatusMock = vi.fn().mockResolvedValue({ tenant: { ...suspendedTenant, status: 1 } })

    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        retrieveTenant: vi.fn().mockResolvedValue({ tenant: suspendedTenant }),
        getTenantProvisioningStatus: vi.fn().mockResolvedValue({ ...mockProvisioningStatus, overallStatus: 2 }),
        updateTenantStatus: updateStatusMock,
        listTenants: vi.fn(),
        initiateTenant: vi.fn(),
        reconcileMigrations: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^activate$/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /^activate$/i }))

    await waitFor(() => {
      expect(updateStatusMock).toHaveBeenCalledWith(
        expect.objectContaining({ tenantId: 'acme_corp', status: 1 }), // ACTIVE = 1
      )
    })
  })
})

describe('TenantDetailPage - deprovision confirmation', () => {
  it('opens confirmation dialog when Deprovision is clicked', async () => {
    vi.mocked(useApiClients).mockReturnValue(
      makeTenantApi() as unknown as ReturnType<typeof useApiClients>,
    )

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /deprovision/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /deprovision/i }))

    await waitFor(() => {
      expect(screen.getByText(/This action cannot be undone/i)).toBeInTheDocument()
    })
  })

  it('disables confirm button until slug is typed correctly', async () => {
    vi.mocked(useApiClients).mockReturnValue(
      makeTenantApi() as unknown as ReturnType<typeof useApiClients>,
    )

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /deprovision/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /deprovision/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /confirm deprovision/i })).toBeDisabled()
    })

    await userEvent.type(screen.getByPlaceholderText('acme-corp'), 'acme-corp')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /confirm deprovision/i })).toBeEnabled()
    })
  })

  it('calls UpdateTenantStatus with DEPROVISIONED after slug confirmation', async () => {
    const updateStatusMock = vi.fn().mockResolvedValue({ tenant: { ...mockActiveTenant, status: 3 } })

    vi.mocked(useApiClients).mockReturnValue({
      tenant: {
        retrieveTenant: vi.fn().mockResolvedValue({ tenant: mockActiveTenant }),
        getTenantProvisioningStatus: vi.fn().mockResolvedValue(mockProvisioningStatus),
        updateTenantStatus: updateStatusMock,
        listTenants: vi.fn(),
        initiateTenant: vi.fn(),
        reconcileMigrations: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /deprovision/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /deprovision/i }))
    await userEvent.type(screen.getByPlaceholderText('acme-corp'), 'acme-corp')
    await userEvent.click(screen.getByRole('button', { name: /confirm deprovision/i }))

    await waitFor(() => {
      expect(updateStatusMock).toHaveBeenCalledWith(
        expect.objectContaining({ tenantId: 'acme_corp', status: 3 }), // DEPROVISIONED = 3
      )
    })
  })
})

describe('TenantDetailPage - back navigation', () => {
  beforeEach(() => {
    vi.mocked(useApiClients).mockReturnValue(
      makeTenantApi() as unknown as ReturnType<typeof useApiClients>,
    )
  })

  it('has a back link to tenants list', async () => {
    renderTenantDetailPage('acme_corp', createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('link', { name: /back to tenants|tenants/i })).toBeInTheDocument()
    })
  })
})
