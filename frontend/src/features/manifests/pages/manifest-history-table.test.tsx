import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ManifestHistoryTable } from './manifest-history-table'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

const mockVersions = [
  {
    id: 'mv-1',
    version: '1.0',
    manifest: { version: '1.0', metadata: { name: 'Test' } },
    appliedAt: { seconds: BigInt(1700000000), nanos: 0 },
    appliedBy: 'admin@example.com',
    applyStatus: ApplyStatus.APPLIED,
    applyJobId: 'job-1',
    diffSummary: 'Initial manifest',
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
  },
  {
    id: 'mv-2',
    version: '1.1',
    manifest: { version: '1.1', metadata: { name: 'Test v1.1' } },
    appliedAt: { seconds: BigInt(1700001000), nanos: 0 },
    appliedBy: 'ops@example.com',
    applyStatus: ApplyStatus.FAILED,
    applyJobId: 'job-2',
    diffSummary: 'Added instrument',
    createdAt: { seconds: BigInt(1700001000), nanos: 0 },
  },
  {
    id: 'mv-3',
    version: '1.2',
    manifest: { version: '1.2', metadata: { name: 'Test v1.2' } },
    appliedAt: { seconds: BigInt(1700002000), nanos: 0 },
    appliedBy: 'dev@example.com',
    applyStatus: ApplyStatus.ROLLED_BACK,
    applyJobId: undefined,
    diffSummary: undefined,
    createdAt: { seconds: BigInt(1700002000), nanos: 0 },
  },
]

function mockApiClients(overrides: Record<string, unknown> = {}) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: vi.fn(),
      listManifestVersions: vi.fn().mockResolvedValue({
        versions: mockVersions,
        totalCount: 3,
      }),
      getManifestVersion: vi.fn(),
      ...overrides,
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderComponent() {
  return renderWithProviders(
    <MemoryRouter>
      <ManifestHistoryTable />
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('ManifestHistoryTable', () => {
  beforeEach(() => mockApiClients())

  it('renders table with version column', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('1.0')).toBeInTheDocument()
    })
    expect(screen.getByText('1.1')).toBeInTheDocument()
    expect(screen.getByText('1.2')).toBeInTheDocument()
  })

  it('renders appliedBy column', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('admin@example.com')).toBeInTheDocument()
    })
    expect(screen.getByText('ops@example.com')).toBeInTheDocument()
  })

  it('renders status badges', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('APPLIED')).toBeInTheDocument()
    })
    expect(screen.getByText('FAILED')).toBeInTheDocument()
    expect(screen.getByText('ROLLED BACK')).toBeInTheDocument()
  })

  it('renders diff summary', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('Initial manifest')).toBeInTheDocument()
    })
    expect(screen.getByText('Added instrument')).toBeInTheDocument()
  })

  it('shows empty state when no versions exist', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        listManifestVersions: vi.fn().mockResolvedValue({ versions: [], totalCount: 0 }),
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
        listManifestVersions: vi.fn().mockRejectedValue(new Error('Server Error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderComponent()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })
  })

  it('opens detail dialog on row click', async () => {
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('1.0')).toBeInTheDocument()
    })

    await userEvent.click(screen.getByText('1.0'))

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
    expect(screen.getByText(/Manifest Version 1.0/)).toBeInTheDocument()
  })
})
