import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ApplyManifestStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import { ApplyResourceModal } from '../apply-resource-modal'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

function mockApiClients(applyResource = vi.fn().mockResolvedValue({
  status: ApplyManifestStatus.APPLIED,
  stepResults: [],
  validationErrors: [],
  diffSummary: '',
  sequenceNumber: BigInt(1),
})) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestApplier: { applyResource },
  } as unknown as ReturnType<typeof useApiClients>)
  return { applyResource }
}

describe('ApplyResourceModal', () => {
  const user = userEvent.setup()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders form fields for instrument type', () => {
    mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
      />,
      { initialToken: createTenantUserToken() },
    )

    expect(screen.getByLabelText('Code')).toBeInTheDocument()
    expect(screen.getByLabelText('Name')).toBeInTheDocument()
    expect(screen.getByLabelText('Type')).toBeInTheDocument()
    expect(screen.getByLabelText('Unit')).toBeInTheDocument()
    expect(screen.getByLabelText('Precision')).toBeInTheDocument()
  })

  it('renders form fields for account_type', () => {
    mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="account_type"
      />,
      { initialToken: createTenantUserToken() },
    )

    expect(screen.getByLabelText('Code')).toBeInTheDocument()
    expect(screen.getByLabelText('Normal Balance')).toBeInTheDocument()
    expect(screen.getByLabelText('Allowed Instruments')).toBeInTheDocument()
  })

  it('shows unsupported message for operational_gateway', () => {
    mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="operational_gateway"
      />,
      { initialToken: createTenantUserToken() },
    )

    expect(screen.getByText(/not supported/i)).toBeInTheDocument()
  })

  it('disables Apply when required fields are empty', () => {
    mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
      />,
      { initialToken: createTenantUserToken() },
    )

    const applyBtn = screen.getByTestId('apply-resource-submit')
    expect(applyBtn).toBeDisabled()
  })

  it('enables Apply when required fields are filled', async () => {
    mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
      />,
      { initialToken: createTenantUserToken() },
    )

    await user.type(screen.getByLabelText('Code'), 'GBP')
    await user.type(screen.getByLabelText('Name'), 'British Pound')
    await user.selectOptions(screen.getByLabelText('Type'), '1')

    const applyBtn = screen.getByTestId('apply-resource-submit')
    expect(applyBtn).toBeEnabled()
  })

  it('calls applyResource with correct payload on submit', async () => {
    const { applyResource } = mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
      />,
      { initialToken: createTenantUserToken() },
    )

    await user.type(screen.getByLabelText('Code'), 'GBP')
    await user.type(screen.getByLabelText('Name'), 'British Pound')
    await user.selectOptions(screen.getByLabelText('Type'), '1')
    await user.click(screen.getByTestId('apply-resource-submit'))

    await waitFor(() => {
      expect(applyResource).toHaveBeenCalledWith(
        expect.objectContaining({
          resourceType: 1,
          dryRun: false,
          resource: expect.objectContaining({
            case: 'instrument',
          }),
        }),
      )
    })
  })

  it('shows success message after apply', async () => {
    mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
      />,
      { initialToken: createTenantUserToken() },
    )

    await user.type(screen.getByLabelText('Code'), 'GBP')
    await user.type(screen.getByLabelText('Name'), 'British Pound')
    await user.selectOptions(screen.getByLabelText('Type'), '1')
    await user.click(screen.getByTestId('apply-resource-submit'))

    await waitFor(() => {
      expect(screen.getByTestId('apply-resource-success')).toBeInTheDocument()
    })
  })

  it('shows error message on failure', async () => {
    mockApiClients(
      vi.fn().mockRejectedValue(new Error('Network error')),
    )
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
      />,
      { initialToken: createTenantUserToken() },
    )

    await user.type(screen.getByLabelText('Code'), 'GBP')
    await user.type(screen.getByLabelText('Name'), 'British Pound')
    await user.selectOptions(screen.getByLabelText('Type'), '1')
    await user.click(screen.getByTestId('apply-resource-submit'))

    await waitFor(() => {
      expect(screen.getByTestId('apply-resource-error')).toBeInTheDocument()
      expect(screen.getByText('Network error')).toBeInTheDocument()
    })
  })

  it('pre-fills form when initialData is provided', () => {
    mockApiClients()
    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
        initialData={{
          code: 'GBP',
          name: 'British Pound',
          type: 1,
          dimensions: { unit: 'GBP', precision: 2 },
        }}
      />,
      { initialToken: createTenantUserToken() },
    )

    expect(screen.getByLabelText('Code')).toHaveValue('GBP')
    expect(screen.getByLabelText('Name')).toHaveValue('British Pound')
    expect(screen.getByLabelText('Unit')).toHaveValue('GBP')
    expect(screen.getByLabelText('Precision')).toHaveValue(2)
  })

  it('shows validation error from server response', async () => {
    mockApiClients(
      vi.fn().mockResolvedValue({
        status: ApplyManifestStatus.VALIDATION_FAILED,
        stepResults: [],
        validationErrors: [
          {
            severity: 'ERROR',
            path: 'instruments[0].code',
            code: 'DUPLICATE',
            message: 'Instrument code already exists',
            suggestion: '',
            resourceType: 'instrument',
            resourceId: 'GBP',
          },
        ],
        diffSummary: '',
        sequenceNumber: BigInt(0),
      }),
    )

    renderWithProviders(
      <ApplyResourceModal
        open={true}
        onOpenChange={vi.fn()}
        nodeType="instrument"
      />,
      { initialToken: createTenantUserToken() },
    )

    await user.type(screen.getByLabelText('Code'), 'GBP')
    await user.type(screen.getByLabelText('Name'), 'British Pound')
    await user.selectOptions(screen.getByLabelText('Type'), '1')
    await user.click(screen.getByTestId('apply-resource-submit'))

    await waitFor(() => {
      expect(screen.getByTestId('apply-resource-error')).toBeInTheDocument()
      expect(screen.getByText('Instrument code already exists')).toBeInTheDocument()
    })
  })
})
