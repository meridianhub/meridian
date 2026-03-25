import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EditorGraphPanel } from './editor-graph-panel'

vi.mock('@/features/manifests/components/manifest-graph', () => ({
  ManifestGraph: ({ manifest, className }: { manifest: unknown; className?: string }) => (
    <div data-testid="manifest-graph" data-manifest={JSON.stringify(manifest)} className={className}>
      MockManifestGraph
    </div>
  ),
}))

const mockManifest = { id: 'manifest-1', name: 'Test Manifest' } as never

describe('EditorGraphPanel - null manifest', () => {
  it('shows placeholder text when manifest is null', () => {
    render(<EditorGraphPanel manifest={null} validationPassed={true} />)

    expect(screen.getByText('No valid manifest to visualize')).toBeInTheDocument()
  })

  it('does not render ManifestGraph when manifest is null', () => {
    render(<EditorGraphPanel manifest={null} validationPassed={true} />)

    expect(screen.queryByTestId('manifest-graph')).not.toBeInTheDocument()
  })

  it('applies className to placeholder container', () => {
    const { container } = render(
      <EditorGraphPanel manifest={null} validationPassed={true} className="custom-class" />,
    )

    expect(container.firstChild).toHaveClass('custom-class')
  })
})

describe('EditorGraphPanel - valid manifest', () => {
  it('renders ManifestGraph when manifest is provided', () => {
    render(<EditorGraphPanel manifest={mockManifest} validationPassed={true} />)

    expect(screen.getByTestId('manifest-graph')).toBeInTheDocument()
  })

  it('does not show stale warning when validation passed', () => {
    render(<EditorGraphPanel manifest={mockManifest} validationPassed={true} />)

    expect(screen.queryByText(/stale/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/fix validation/i)).not.toBeInTheDocument()
  })

  it('applies className to wrapper container', () => {
    const { container } = render(
      <EditorGraphPanel manifest={mockManifest} validationPassed={true} className="my-class" />,
    )

    expect(container.firstChild).toHaveClass('my-class')
  })

  it('passes full-size class to ManifestGraph', () => {
    render(<EditorGraphPanel manifest={mockManifest} validationPassed={true} />)

    const graph = screen.getByTestId('manifest-graph')
    expect(graph).toHaveClass('h-full')
    expect(graph).toHaveClass('w-full')
  })
})

describe('EditorGraphPanel - validation overlay', () => {
  it('shows stale warning overlay when validation has not passed', () => {
    render(<EditorGraphPanel manifest={mockManifest} validationPassed={false} />)

    expect(screen.getByText(/graph stale/i)).toBeInTheDocument()
    expect(screen.getByText(/fix validation errors to update/i)).toBeInTheDocument()
  })

  it('applies blur overlay when validation has not passed', () => {
    render(<EditorGraphPanel manifest={mockManifest} validationPassed={false} />)

    const overlay = screen.getByText(/graph stale/i).closest('div')
    expect(overlay).toHaveClass('backdrop-blur-[1px]')
  })

  it('renders ManifestGraph but dims it when validation has not passed', () => {
    render(<EditorGraphPanel manifest={mockManifest} validationPassed={false} />)

    expect(screen.getByTestId('manifest-graph')).toBeInTheDocument()

    // The wrapper div around the graph should have pointer-events-none and opacity-50
    const graph = screen.getByTestId('manifest-graph')
    const graphWrapper = graph.parentElement
    expect(graphWrapper).toHaveClass('pointer-events-none')
    expect(graphWrapper).toHaveClass('opacity-50')
  })

  it('does not dim graph wrapper when validation has passed', () => {
    render(<EditorGraphPanel manifest={mockManifest} validationPassed={true} />)

    const graph = screen.getByTestId('manifest-graph')
    const graphWrapper = graph.parentElement
    expect(graphWrapper).not.toHaveClass('pointer-events-none')
    expect(graphWrapper).not.toHaveClass('opacity-50')
  })
})
