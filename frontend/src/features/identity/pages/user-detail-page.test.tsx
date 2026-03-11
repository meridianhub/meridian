import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createPlatformAdminToken } from '@/test/jwt-helpers'
import { IdentityStatus, Role } from '@/api/gen/meridian/identity/v1/identity_pb'
import { UserDetailPage } from './user-detail-page'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

const mockIdentity = {
  id: 'user-1',
  email: 'alice@example.com',
  externalIdp: '',
  externalIdpSub: '',
  status: IdentityStatus.ACTIVE,
  lastLoginAt: { seconds: BigInt(1700000000), nanos: 0 },
  failedAttempts: 0,
  lockedUntil: undefined,
  mfaEnabled: true,
  createdAt: { seconds: BigInt(1699000000), nanos: 0 },
  updatedAt: { seconds: BigInt(1700000000), nanos: 0 },
  version: 1,
}

const mockRoleAssignments = [
  {
    id: 'ra-1',
    identityId: 'user-1',
    role: Role.ADMIN,
    grantedBy: 'admin-1',
    grantedAt: { seconds: BigInt(1699000000), nanos: 0 },
    expiresAt: undefined,
    revoked: false,
    revokedAt: undefined,
    revokedBy: '',
  },
]

function mockApiClients() {
  vi.mocked(useApiClients).mockReturnValue({
    identity: {
      retrieveIdentity: vi.fn().mockResolvedValue({ identity: mockIdentity }),
      listRoleAssignments: vi.fn().mockResolvedValue({
        roleAssignments: mockRoleAssignments,
      }),
      suspendIdentity: vi.fn().mockResolvedValue({
        identity: { ...mockIdentity, status: IdentityStatus.SUSPENDED },
      }),
      reactivateIdentity: vi.fn().mockResolvedValue({
        identity: { ...mockIdentity, status: IdentityStatus.ACTIVE },
      }),
      grantRole: vi.fn().mockResolvedValue({ roleAssignment: mockRoleAssignments[0] }),
      revokeRole: vi.fn().mockResolvedValue({ roleAssignment: mockRoleAssignments[0] }),
      listIdentities: vi.fn(),
      inviteUser: vi.fn(),
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderUserDetailPage(token: string) {
  return renderWithProviders(
    <MemoryRouter initialEntries={['/users/user-1']}>
      <Routes>
        <Route path="/users/:userId" element={<UserDetailPage />} />
        <Route path="/users" element={<div data-testid="users-list">List</div>} />
      </Routes>
    </MemoryRouter>,
    { initialToken: token },
  )
}

describe('UserDetailPage', () => {
  beforeEach(() => mockApiClients())

  it('renders user email as heading', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'alice@example.com' })).toBeInTheDocument()
    })
  })

  it('shows user ID', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText(/ID: user-1/)).toBeInTheDocument()
    })
  })

  it('shows identity status badge', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
  })

  it('shows MFA status', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('Enabled')).toBeInTheDocument()
    })
  })

  it('shows suspend button for active users', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /suspend/i })).toBeInTheDocument()
    })
  })

  it('opens suspend dialog on button click', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /suspend/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /suspend/i }))

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText('Suspending this user will prevent them from logging in.')).toBeInTheDocument()
  })

  it('shows role assignments table', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('Admin')).toBeInTheDocument()
    })
  })

  it('shows breadcrumb navigation to users list', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('navigation', { name: /breadcrumb/i })).toBeInTheDocument()
    })
    expect(screen.getByText('Users')).toBeInTheDocument()
  })

  it('shows grant role button', async () => {
    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /grant role/i })).toBeInTheDocument()
    })
  })
})

describe('UserDetailPage - suspended user', () => {
  it('shows reactivate button for suspended users', async () => {
    const suspendedIdentity = { ...mockIdentity, status: IdentityStatus.SUSPENDED }
    vi.mocked(useApiClients).mockReturnValue({
      identity: {
        retrieveIdentity: vi.fn().mockResolvedValue({ identity: suspendedIdentity }),
        listRoleAssignments: vi.fn().mockResolvedValue({ roleAssignments: [] }),
        suspendIdentity: vi.fn(),
        reactivateIdentity: vi.fn(),
        grantRole: vi.fn(),
        revokeRole: vi.fn(),
        listIdentities: vi.fn(),
        inviteUser: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /reactivate/i })).toBeInTheDocument()
    })
  })
})

describe('UserDetailPage - error state', () => {
  it('shows error state when API fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      identity: {
        retrieveIdentity: vi.fn().mockRejectedValue(new Error('Not Found')),
        listRoleAssignments: vi.fn().mockResolvedValue({ roleAssignments: [] }),
        suspendIdentity: vi.fn(),
        reactivateIdentity: vi.fn(),
        grantRole: vi.fn(),
        revokeRole: vi.fn(),
        listIdentities: vi.fn(),
        inviteUser: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderUserDetailPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })
})
