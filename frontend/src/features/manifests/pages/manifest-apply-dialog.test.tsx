import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ManifestApplyDialog } from './manifest-apply-dialog'
import { ApplyManifestStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

// The component calls create(ManifestSchema, parsed) before calling applyManifest.
// In tests, ManifestSchema is a stub that doesn't satisfy @bufbuild/protobuf's create().
// We mock the entire module to return the parsed object directly.
vi.mock('@bufbuild/protobuf', () => ({
  create: (_schema: unknown, data: unknown) => data,
}))

import { useApiClients } from '@/api/context'

const mockDryRunResponse = {
  jobId: 'job-123',
  status: ApplyManifestStatus.DRY_RUN,
  stepResults: [
    { stepName: 'validate', status: 1, message: 'Validation passed', details: {} },
    { stepName: 'diff', status: 1, message: '2 changes detected', details: {} },
    { stepName: 'execute', status: 3, message: 'Skipped (dry run)', details: {} },
  ],
  snapshot: undefined,
  validationErrors: [],
  diffSummary: 'Added 2 instruments, 1 account type',
}

const mockApplyResponse = {
  jobId: 'job-456',
  status: ApplyManifestStatus.APPLIED,
  stepResults: [],
  snapshot: undefined,
  validationErrors: [],
  diffSummary: 'Applied successfully',
}

let applyManifestMock: ReturnType<typeof vi.fn>

function mockApiClients() {
  applyManifestMock = vi.fn()
  vi.mocked(useApiClients).mockReturnValue({
    manifestApplier: {
      applyManifest: applyManifestMock,
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderDialog(open = true) {
  const onOpenChange = vi.fn()
  renderWithProviders(
    <MemoryRouter>
      <ManifestApplyDialog open={open} onOpenChange={onOpenChange} />
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
  return { onOpenChange }
}

const validManifest = JSON.stringify({
  version: '1.0',
  metadata: { name: 'Test', industry: 'test', description: 'Test' },
  instruments: [],
  accountTypes: [],
})

describe('ManifestApplyDialog', () => {
  beforeEach(() => mockApiClients())

  it('renders dialog when open=true', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('does not render dialog when open=false', () => {
    renderDialog(false)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders JSON textarea', () => {
    renderDialog()
    expect(screen.getByLabelText(/manifest json/i)).toBeInTheDocument()
  })

  it('Preview button disabled when JSON is empty', () => {
    renderDialog()
    const previewBtn = screen.getByRole('button', { name: /preview changes/i })
    expect(previewBtn).toBeDisabled()
  })

  it('Apply button disabled until preview succeeds', () => {
    renderDialog()
    const applyBtn = screen.getByRole('button', { name: /apply manifest/i })
    expect(applyBtn).toBeDisabled()
  })

  it('calls ApplyManifest with dry_run=true on Preview', async () => {
    applyManifestMock.mockResolvedValue(mockDryRunResponse)
    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: validManifest } })

    // Use fireEvent.click because Radix Dialog sets pointer-events:none on body in jsdom
    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      expect(applyManifestMock).toHaveBeenCalledWith(
        expect.objectContaining({ dryRun: true }),
      )
    })
  })

  it('displays dry run result panel after preview', async () => {
    applyManifestMock.mockResolvedValue(mockDryRunResponse)
    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: validManifest } })

    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      expect(screen.getByTestId('dry-run-result')).toBeInTheDocument()
    })
    expect(screen.getByText(/Added 2 instruments, 1 account type/)).toBeInTheDocument()
  })

  it('displays step results in dry run panel', async () => {
    applyManifestMock.mockResolvedValue(mockDryRunResponse)
    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: validManifest } })

    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      expect(screen.getByText('validate')).toBeInTheDocument()
    })
    expect(screen.getByText('diff')).toBeInTheDocument()
  })

  it('enables Apply button after successful preview', async () => {
    applyManifestMock.mockResolvedValue(mockDryRunResponse)
    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: validManifest } })

    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      const applyBtn = screen.getByRole('button', { name: /apply manifest/i })
      expect(applyBtn).not.toBeDisabled()
    })
  })

  it('calls ApplyManifest with dry_run=false on Apply', async () => {
    applyManifestMock
      .mockResolvedValueOnce(mockDryRunResponse)
      .mockResolvedValueOnce(mockApplyResponse)

    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: validManifest } })

    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      expect(screen.getByTestId('dry-run-result')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /apply manifest/i }))

    await waitFor(() => {
      expect(applyManifestMock).toHaveBeenCalledWith(
        expect.objectContaining({ dryRun: false }),
      )
    })
  })

  it('shows parse error for invalid JSON', async () => {
    applyManifestMock.mockRejectedValue(new Error('Unexpected token'))
    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: 'not-json' } })

    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      expect(screen.getByTestId('parse-error')).toBeInTheDocument()
    })
  })

  it('shows validation errors when preview returns VALIDATION_FAILED', async () => {
    applyManifestMock.mockResolvedValue({
      ...mockDryRunResponse,
      status: ApplyManifestStatus.VALIDATION_FAILED,
      validationErrors: [
        {
          severity: 'ERROR',
          path: 'instruments[0].code',
          code: 'INVALID_CODE',
          message: 'Code must be uppercase',
          suggestion: 'Use GBP',
        },
      ],
    })

    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: validManifest } })

    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      expect(screen.getByTestId('validation-errors')).toBeInTheDocument()
    })
    expect(screen.getByText(/Code must be uppercase/)).toBeInTheDocument()
  })

  it('Apply button stays disabled after validation failure', async () => {
    applyManifestMock.mockResolvedValue({
      ...mockDryRunResponse,
      status: ApplyManifestStatus.VALIDATION_FAILED,
      validationErrors: [
        { severity: 'ERROR', path: 'instruments[0]', code: 'ERR', message: 'Bad', suggestion: '' },
      ],
    })

    renderDialog()

    const textarea = screen.getByLabelText(/manifest json/i)
    fireEvent.change(textarea, { target: { value: validManifest } })

    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => {
      expect(screen.getByTestId('validation-errors')).toBeInTheDocument()
    })

    const applyBtn = screen.getByRole('button', { name: /apply manifest/i })
    expect(applyBtn).toBeDisabled()
  })
})
