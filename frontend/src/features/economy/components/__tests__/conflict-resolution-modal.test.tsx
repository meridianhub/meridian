import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/test-utils'
import { ConflictResolutionModal } from '../conflict-resolution-modal'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

// Mock the ManifestDiffGraph to avoid ReactFlow/ELK complexity in unit tests
vi.mock('@/features/manifests/components/manifest-diff-graph', () => ({
  ManifestDiffGraph: ({ before, after }: { before: unknown; after: unknown }) => (
    <div data-testid="manifest-diff-graph">
      Diff: {JSON.stringify(before)} vs {JSON.stringify(after)}
    </div>
  ),
}))

vi.mock('@/features/manifests/lib/manifest-graph-model', () => ({
  buildManifestGraph: (manifest: unknown) => ({ nodes: [], edges: [], manifest }),
}))

const userManifest = { $typeName: 'meridian.control_plane.v1.Manifest' } as unknown as Manifest
const serverManifest = { $typeName: 'meridian.control_plane.v1.Manifest', instruments: [] } as unknown as Manifest

describe('ConflictResolutionModal', () => {
  const user = userEvent.setup()

  it('renders with version conflict message when open', () => {
    renderWithProviders(
      <ConflictResolutionModal
        open={true}
        onResolve={vi.fn()}
        userManifest={userManifest}
        serverManifest={serverManifest}
      />,
    )

    expect(screen.getByText('Version Conflict')).toBeInTheDocument()
    expect(screen.getByText(/modified by another user/)).toBeInTheDocument()
  })

  it('renders the diff graph', () => {
    renderWithProviders(
      <ConflictResolutionModal
        open={true}
        onResolve={vi.fn()}
        userManifest={userManifest}
        serverManifest={serverManifest}
      />,
    )

    expect(screen.getByTestId('conflict-diff-graph')).toBeInTheDocument()
    expect(screen.getByTestId('manifest-diff-graph')).toBeInTheDocument()
  })

  it('calls onResolve with "force" when Force Apply is clicked', async () => {
    const onResolve = vi.fn()
    renderWithProviders(
      <ConflictResolutionModal
        open={true}
        onResolve={onResolve}
        userManifest={userManifest}
        serverManifest={serverManifest}
      />,
    )

    await user.click(screen.getByTestId('conflict-force'))
    expect(onResolve).toHaveBeenCalledWith('force')
  })

  it('calls onResolve with "reload" when Reload is clicked', async () => {
    const onResolve = vi.fn()
    renderWithProviders(
      <ConflictResolutionModal
        open={true}
        onResolve={onResolve}
        userManifest={userManifest}
        serverManifest={serverManifest}
      />,
    )

    await user.click(screen.getByTestId('conflict-reload'))
    expect(onResolve).toHaveBeenCalledWith('reload')
  })

  it('calls onResolve with "cancel" when Cancel is clicked', async () => {
    const onResolve = vi.fn()
    renderWithProviders(
      <ConflictResolutionModal
        open={true}
        onResolve={onResolve}
        userManifest={userManifest}
        serverManifest={serverManifest}
      />,
    )

    await user.click(screen.getByTestId('conflict-cancel'))
    expect(onResolve).toHaveBeenCalledWith('cancel')
  })

  it('does not render when open is false', () => {
    renderWithProviders(
      <ConflictResolutionModal
        open={false}
        onResolve={vi.fn()}
        userManifest={userManifest}
        serverManifest={serverManifest}
      />,
    )

    expect(screen.queryByText('Version Conflict')).not.toBeInTheDocument()
  })
})
