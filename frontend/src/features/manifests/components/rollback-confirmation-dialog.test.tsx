import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { axe } from '@/test/test-utils'
import { RollbackConfirmationDialog } from './rollback-confirmation-dialog'
import type { ManifestVersion } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { RollbackStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

vi.mock('../hooks/use-rollback-manifest', () => ({
  useRollbackManifest: vi.fn(),
}))

import { useRollbackManifest } from '../hooks/use-rollback-manifest'
const mockUseRollbackManifest = vi.mocked(useRollbackManifest)

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TenantProvider>
          <TooltipProvider>
            <MemoryRouter>{children}</MemoryRouter>
          </TooltipProvider>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

function makeMockMutation(overrides: Partial<ReturnType<typeof useRollbackManifest>> = {}) {
  return {
    mutateAsync: vi.fn(),
    isPending: false,
    isError: false,
    error: null,
    reset: vi.fn(),
    ...overrides,
  } as unknown as ReturnType<typeof useRollbackManifest>
}

function makeVersion(overrides: Partial<ManifestVersion> = {}): ManifestVersion {
  return {
    id: 'ver-001',
    version: '1.0',
    sequenceNumber: BigInt(5),
    appliedBy: 'test-user',
    appliedAt: { seconds: BigInt(1700000000), nanos: 0 },
    applyStatus: 0,
    ...overrides,
  } as unknown as ManifestVersion
}

describe('RollbackConfirmationDialog - rendering', () => {
  beforeEach(() => {
    mockUseRollbackManifest.mockReturnValue(makeMockMutation())
  })

  it('does not render dialog content when closed', () => {
    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={false}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog with title and description when open', () => {
    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /rollback manifest/i })).toBeInTheDocument()
    expect(screen.getByText(/revert to version/i)).toBeInTheDocument()
  })

  it('renders version details when version is provided', () => {
    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion({ version: '2.3', sequenceNumber: BigInt(7) })}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByText('2.3')).toBeInTheDocument()
    expect(screen.getByText(/sequence #7/i)).toBeInTheDocument()
  })

  it('renders Preview and Rollback buttons', () => {
    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByTestId('rollback-preview-button')).toBeInTheDocument()
    expect(screen.getByTestId('rollback-confirm-button')).toBeInTheDocument()
  })

  it('handles null version without crashing - version details section not rendered', () => {
    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={null}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    // The version details section (applied by, originally applied) is only shown when version is non-null
    expect(screen.queryByText(/originally applied/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/applied by/i)).not.toBeInTheDocument()
  })
})

describe('RollbackConfirmationDialog - preview flow', () => {
  it('calls mutateAsync with dryRun=true when preview is clicked', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ message: 'No changes detected', status: RollbackStatus.DRY_RUN })
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion({ sequenceNumber: BigInt(5) })}
          appliedBy="system"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith({
        targetSequenceNumber: BigInt(5),
        dryRun: true,
        appliedBy: 'system',
      })
    })
  })

  it('displays preview message from response', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ message: 'Dry run successful: 3 changes', status: RollbackStatus.DRY_RUN })
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))

    await waitFor(() => {
      expect(screen.getByTestId('rollback-preview')).toHaveTextContent('Dry run successful: 3 changes')
    })
  })

  it('shows fallback message when response message is empty', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ message: '', status: RollbackStatus.DRY_RUN })
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))

    await waitFor(() => {
      expect(screen.getByTestId('rollback-preview')).toHaveTextContent('Preview complete')
    })
  })

  it('shows loading state during preview', async () => {
    const user = userEvent.setup()
    let resolvePreview!: (val: unknown) => void
    const mutateAsync = vi.fn().mockImplementation(
      () => new Promise((resolve) => { resolvePreview = resolve }),
    )
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))

    expect(screen.getByTestId('rollback-preview-button')).toHaveTextContent('Previewing...')
    expect(screen.getByTestId('rollback-preview-button')).toBeDisabled()

    await act(async () => {
      resolvePreview({ message: 'done', status: RollbackStatus.DRY_RUN })
    })
  })

  it('shows error message when preview throws', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockRejectedValue(new Error('preview failed'))
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))

    await waitFor(() => {
      expect(screen.getByTestId('rollback-preview')).toHaveTextContent('Preview failed: preview failed')
    })
  })
})

describe('RollbackConfirmationDialog - rollback flow', () => {
  it('calls mutateAsync with dryRun=false when rollback is clicked', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ message: 'done', status: RollbackStatus.COMPLETED })
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion({ sequenceNumber: BigInt(3) })}
          appliedBy="admin"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-confirm-button'))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith({
        targetSequenceNumber: BigInt(3),
        dryRun: false,
        appliedBy: 'admin',
      })
    })
  })

  it('closes dialog when rollback status is COMPLETED', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const mutateAsync = vi.fn().mockResolvedValue({ message: 'applied', status: RollbackStatus.COMPLETED })
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={onOpenChange}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-confirm-button'))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('does not close dialog when rollback status is NO_CHANGE', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const mutateAsync = vi.fn().mockResolvedValue({ message: 'identical', status: RollbackStatus.NO_CHANGE })
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={onOpenChange}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-confirm-button'))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalled()
    })
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('shows loading state during rollback', () => {
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ isPending: true }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByTestId('rollback-confirm-button')).toHaveTextContent('Rolling back...')
    expect(screen.getByTestId('rollback-confirm-button')).toBeDisabled()
  })

  it('disables preview button while rollback is pending', () => {
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ isPending: true }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByTestId('rollback-preview-button')).toBeDisabled()
  })
})

describe('RollbackConfirmationDialog - error handling', () => {
  it('displays mutation error message', () => {
    const error = new Error('rollback failed: conflict')
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ error }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByTestId('rollback-error')).toHaveTextContent('rollback failed: conflict')
  })

  it('does not render error element when error is null', () => {
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ error: null }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.queryByTestId('rollback-error')).not.toBeInTheDocument()
  })

  it('shows stringified error in preview message for non-Error thrown values', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockRejectedValue('plain string error')
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))

    await waitFor(() => {
      expect(screen.getByTestId('rollback-preview')).toHaveTextContent('Preview failed: plain string error')
    })
  })
})

describe('RollbackConfirmationDialog - cancel and close behavior', () => {
  it('calls reset when dialog is closed', async () => {
    const user = userEvent.setup()
    const reset = vi.fn()
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ reset }))
    const onOpenChange = vi.fn()

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={onOpenChange}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(reset).toHaveBeenCalled()
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('clears preview message when dialog closes via user interaction', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ message: 'preview ok', status: RollbackStatus.DRY_RUN })
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))
    await waitFor(() => {
      expect(screen.getByTestId('rollback-preview')).toBeInTheDocument()
    })

    // Pressing Escape triggers handleClose which clears state
    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(screen.queryByTestId('rollback-preview')).not.toBeInTheDocument()
    })
  })

  it('does not update state after dialog is closed during async operation', async () => {
    const user = userEvent.setup()
    let resolvePreview!: (val: unknown) => void
    const mutateAsync = vi.fn().mockImplementation(
      () => new Promise((resolve) => { resolvePreview = resolve }),
    )
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))
    const onOpenChange = vi.fn()

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={onOpenChange}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    // Start preview
    await user.click(screen.getByTestId('rollback-preview-button'))

    // Close the dialog while preview is in-flight
    await user.keyboard('{Escape}')

    // Resolve after close - should not update state
    await act(async () => {
      resolvePreview({ message: 'late response', status: RollbackStatus.DRY_RUN })
    })

    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('does not call mutateAsync when version is null', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn()
    mockUseRollbackManifest.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={null}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.click(screen.getByTestId('rollback-preview-button'))
    await user.click(screen.getByTestId('rollback-confirm-button'))

    expect(mutateAsync).not.toHaveBeenCalled()
  })
})

describe('RollbackConfirmationDialog - accessibility', () => {
  beforeEach(() => {
    mockUseRollbackManifest.mockReturnValue(makeMockMutation())
  })

  it('has accessible dialog title and description', () => {
    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    expect(screen.getByRole('dialog')).toHaveAccessibleName(/rollback manifest/i)
    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(/revert to version/i)
  })

  it('closes dialog on Escape key', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()

    render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={onOpenChange}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('has no axe violations when open', async () => {
    const { container } = render(
      <Wrapper>
        <RollbackConfirmationDialog
          open={true}
          onOpenChange={vi.fn()}
          version={makeVersion()}
          appliedBy="test-user"
        />
      </Wrapper>,
    )

    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })
})
