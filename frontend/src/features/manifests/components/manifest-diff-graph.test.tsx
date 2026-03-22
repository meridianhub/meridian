import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import { ManifestDiffGraph } from './manifest-diff-graph'
import type { ManifestGraph } from '../lib/manifest-graph-model'

vi.mock('@/components/ui/tooltip', () => ({
  TooltipProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="tooltip-content">{children}</div>
  ),
}))

vi.mock('@xyflow/react', () => {
  const Position = { Top: 'top', Bottom: 'bottom', Left: 'left', Right: 'right' }
  const BackgroundVariant = { Dots: 'dots' }

  function Handle() { return null }

  function ReactFlow({ nodes, edges, children }: {
    nodes: { id: string; type?: string; data: Record<string, unknown> }[]
    edges: { id: string; source: string; target: string; data?: Record<string, unknown> }[]
    children?: React.ReactNode
    [key: string]: unknown
  }) {
    return (
      <div data-testid="react-flow" data-node-count={nodes.length} data-edge-count={edges.length}>
        {nodes.map((n) => (
          <div
            key={n.id}
            data-testid={`diff-flow-node-${n.id}`}
            data-node-type={n.type}
            data-diff-status={(n.data as { diffStatus?: string }).diffStatus}
          >
            {n.id}
          </div>
        ))}
        {edges.map((e) => (
          <div key={e.id} data-testid={`diff-flow-edge-${e.id}`} data-diff-status={(e.data as { diffStatus?: string })?.diffStatus}>
            {e.source} -&gt; {e.target}
          </div>
        ))}
        {children}
      </div>
    )
  }

  function Controls() { return <div data-testid="controls" /> }
  function Background() { return null }

  return {
    ReactFlow,
    Controls,
    Background,
    Handle,
    Position,
    BackgroundVariant,
    useNodesState: (init: unknown[]) => {
      const [s, setS] = useState(init)
      return [s, setS, vi.fn()]
    },
    useEdgesState: (init: unknown[]) => {
      const [s, setS] = useState(init)
      return [s, setS, vi.fn()]
    },
  }
})

vi.mock('elkjs/lib/elk.bundled.js', () => ({
  default: class MockELK {
    async layout(graph: { children: { id: string }[] }) {
      return {
        children: graph.children.map((child, i) => ({
          id: child.id,
          x: i * 100,
          y: i * 100,
        })),
      }
    }
  },
}))

const emptyGraph: ManifestGraph = { nodes: [], edges: [] }

const graphWithInstrument: ManifestGraph = {
  nodes: [
    {
      id: 'instrument:GBP',
      type: 'instrument',
      label: 'British Pound',
      data: { code: 'GBP', dimensions: { unit: 'GBP' } },
    },
  ],
  edges: [],
}

const graphWithTwo: ManifestGraph = {
  nodes: [
    {
      id: 'instrument:GBP',
      type: 'instrument',
      label: 'British Pound',
      data: { code: 'GBP', dimensions: { unit: 'GBP' } },
    },
    {
      id: 'instrument:KWH',
      type: 'instrument',
      label: 'Kilowatt Hour',
      data: { code: 'KWH', dimensions: { unit: 'kWh' } },
    },
  ],
  edges: [],
}

describe('ManifestDiffGraph', () => {
  describe('no-diff state', () => {
    it('shows no-changes message when before and after are identical', () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={graphWithInstrument} />)
      expect(screen.getByTestId('manifest-diff-no-changes')).toBeInTheDocument()
      expect(screen.getByText('No differences between versions.')).toBeInTheDocument()
    })

    it('shows no-changes for two empty graphs', () => {
      render(<ManifestDiffGraph before={emptyGraph} after={emptyGraph} />)
      expect(screen.getByTestId('manifest-diff-no-changes')).toBeInTheDocument()
    })
  })

  describe('graph rendering with changes', () => {
    it('renders ReactFlow when nodes differ', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByTestId('react-flow')).toBeInTheDocument()
    })

    it('renders diff-summary panel', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByTestId('diff-summary')).toBeInTheDocument()
    })

    it('shows added count when node is new', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByText('+1 added')).toBeInTheDocument()
    })

    it('shows removed count when node is removed', async () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={emptyGraph} />)
      expect(await screen.findByText('-1 removed')).toBeInTheDocument()
    })

    it('renders node with added diff status', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      const node = await screen.findByTestId('diff-flow-node-instrument:GBP')
      expect(node).toHaveAttribute('data-diff-status', 'added')
    })

    it('renders node with removed diff status', async () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={emptyGraph} />)
      const node = await screen.findByTestId('diff-flow-node-instrument:GBP')
      expect(node).toHaveAttribute('data-diff-status', 'removed')
    })

    it('renders node with unchanged diff status when present in both', async () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={graphWithInstrument} />)
      // identical graphs => no-diff state, no ReactFlow rendered
      expect(screen.getByTestId('manifest-diff-no-changes')).toBeInTheDocument()
    })

    it('renders multiple added nodes', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithTwo} />)
      const flow = await screen.findByTestId('react-flow')
      expect(flow).toHaveAttribute('data-node-count', '2')
    })

    it('assigns diff_instrument node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      const node = await screen.findByTestId('diff-flow-node-instrument:GBP')
      expect(node).toHaveAttribute('data-node-type', 'diff_instrument')
    })

    it('accepts className prop', async () => {
      const { container } = render(
        <ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} className="h-96" />,
      )
      await screen.findByTestId('react-flow')
      const wrapper = container.querySelector('[data-testid="manifest-diff-graph"]')
      expect(wrapper).toHaveClass('h-96')
    })
  })
})
