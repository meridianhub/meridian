import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ManifestDiffViewer } from './manifest-diff'
import type { ManifestDiff } from '@/features/manifests/lib/manifest-diff'
import type { ManifestNode } from '@/features/manifests/lib/manifest-graph-model'

const makeNode = (id: string, label: string): ManifestNode => ({
  id,
  type: 'instrument',
  label,
  data: { code: id },
})

const emptyDiff: ManifestDiff = {
  addedNodes: [],
  removedNodes: [],
  modifiedNodes: [],
  addedEdges: [],
  removedEdges: [],
}

describe('ManifestDiffViewer - empty diff', () => {
  it('renders nothing when diff is null', () => {
    const { container } = render(<ManifestDiffViewer diff={null} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders no-changes message when all sections are empty', () => {
    render(<ManifestDiffViewer diff={emptyDiff} />)
    expect(screen.getByText(/no changes/i)).toBeInTheDocument()
  })
})

describe('ManifestDiffViewer - added nodes', () => {
  const diff: ManifestDiff = {
    ...emptyDiff,
    addedNodes: [makeNode('instrument:GBP', 'GBP'), makeNode('instrument:USD', 'USD')],
  }

  it('renders Added section with count', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText(/added/i)).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
  })

  it('renders each added node label', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText('GBP')).toBeInTheDocument()
    expect(screen.getByText('USD')).toBeInTheDocument()
  })

  it('calls onNodeClick with node when added item is clicked', async () => {
    const onNodeClick = vi.fn()
    render(<ManifestDiffViewer diff={diff} onNodeClick={onNodeClick} />)
    await userEvent.click(screen.getByText('GBP'))
    expect(onNodeClick).toHaveBeenCalledWith(diff.addedNodes[0])
  })
})

describe('ManifestDiffViewer - removed nodes', () => {
  const diff: ManifestDiff = {
    ...emptyDiff,
    removedNodes: [makeNode('instrument:EUR', 'EUR')],
  }

  it('renders Removed section', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText(/removed/i)).toBeInTheDocument()
  })

  it('renders removed node label', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText('EUR')).toBeInTheDocument()
  })

  it('calls onNodeClick with node when removed item is clicked', async () => {
    const onNodeClick = vi.fn()
    render(<ManifestDiffViewer diff={diff} onNodeClick={onNodeClick} />)
    await userEvent.click(screen.getByText('EUR'))
    expect(onNodeClick).toHaveBeenCalledWith(diff.removedNodes[0])
  })
})

describe('ManifestDiffViewer - modified nodes', () => {
  const beforeNode = makeNode('instrument:GBP', 'GBP Old')
  const afterNode = { ...makeNode('instrument:GBP', 'GBP New'), data: { code: 'GBP', name: 'Updated' } }
  const diff: ManifestDiff = {
    ...emptyDiff,
    modifiedNodes: [{ before: beforeNode, after: afterNode }],
  }

  it('renders Modified section', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText(/modified/i)).toBeInTheDocument()
  })

  it('shows before and after labels for modified nodes', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText('GBP Old')).toBeInTheDocument()
    expect(screen.getByText('GBP New')).toBeInTheDocument()
  })

  it('shows before/after comparison labels', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText(/before/i)).toBeInTheDocument()
    expect(screen.getByText(/after/i)).toBeInTheDocument()
  })
})

describe('ManifestDiffViewer - mixed diff', () => {
  const diff: ManifestDiff = {
    addedNodes: [makeNode('instrument:GBP', 'GBP')],
    removedNodes: [makeNode('instrument:EUR', 'EUR')],
    modifiedNodes: [
      {
        before: makeNode('instrument:USD', 'USD Old'),
        after: makeNode('instrument:USD', 'USD New'),
      },
    ],
    addedEdges: [],
    removedEdges: [],
  }

  it('renders all three sections', () => {
    render(<ManifestDiffViewer diff={diff} />)
    expect(screen.getByText(/added/i)).toBeInTheDocument()
    expect(screen.getByText(/modified/i)).toBeInTheDocument()
    expect(screen.getByText(/removed/i)).toBeInTheDocument()
  })
})
