import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ExecutionSubgraph } from './execution-subgraph'
import { filterSubgraph } from '@/features/reference-data/lib/filter-subgraph'
import type { ManifestGraph } from '@/features/manifests/lib/manifest-graph-model'

vi.mock('@xyflow/react', () => ({
  ReactFlow: ({ children, ...props }: Record<string, unknown>) => (
    <div data-testid="react-flow" {...props}>{children as React.ReactNode}</div>
  ),
  Controls: () => <div data-testid="react-flow-controls" />,
  Background: () => <div data-testid="react-flow-background" />,
  useNodesState: () => [[], vi.fn(), vi.fn()],
  useEdgesState: () => [[], vi.fn(), vi.fn()],
  Position: { Top: 'top', Bottom: 'bottom' },
  Handle: () => null,
  BackgroundVariant: { Dots: 'dots' },
}))

vi.mock('@/lib/visualization/graph-layout', () => ({
  layoutWithELK: vi.fn(async () => []),
  NODE_WIDTH: 180,
  NODE_BASE_HEIGHT: 60,
  NODE_PADDING: 10,
}))

function buildTestGraph(): ManifestGraph {
  return {
    nodes: [
      { id: 'instrument:GBP', type: 'instrument', label: 'British Pound', data: { code: 'GBP' } },
      { id: 'account_type:CUSTOMER', type: 'account_type', label: 'Customer Account', data: { code: 'CUSTOMER' } },
      { id: 'saga:payment_saga', type: 'saga', label: 'payment_saga', data: { name: 'payment_saga', trigger: 'event:tx' } },
      { id: 'instrument:USD', type: 'instrument', label: 'US Dollar', data: { code: 'USD' } },
    ],
    edges: [
      { id: 'writes_to:saga:payment_saga:CUSTOMER', source: 'saga:payment_saga', target: 'account_type:CUSTOMER', relationship: 'writes_to' },
      { id: 'allowed_by:CUSTOMER:GBP', source: 'account_type:CUSTOMER', target: 'instrument:GBP', relationship: 'allowed_by' },
    ],
  }
}

describe('filterSubgraph', () => {
  it('includes focus node and directly connected nodes', () => {
    const graph = buildTestGraph()
    const result = filterSubgraph(graph, 'instrument:GBP')

    const nodeIds = result.nodes.map((n) => n.id)
    expect(nodeIds).toContain('instrument:GBP')
    expect(nodeIds).toContain('account_type:CUSTOMER')
  })

  it('excludes unconnected nodes', () => {
    const graph = buildTestGraph()
    const result = filterSubgraph(graph, 'instrument:GBP')

    const nodeIds = result.nodes.map((n) => n.id)
    expect(nodeIds).not.toContain('instrument:USD')
  })

  it('includes sagas that write to connected nodes', () => {
    const graph = buildTestGraph()
    const result = filterSubgraph(graph, 'instrument:GBP')

    const nodeIds = result.nodes.map((n) => n.id)
    expect(nodeIds).toContain('saga:payment_saga')
  })

  it('returns only focus node when it has no edges', () => {
    const graph = buildTestGraph()
    const result = filterSubgraph(graph, 'instrument:USD')

    expect(result.nodes).toHaveLength(1)
    expect(result.nodes[0].id).toBe('instrument:USD')
    expect(result.edges).toHaveLength(0)
  })
})

describe('ExecutionSubgraph', () => {
  it('renders empty state when graph is null', () => {
    render(<ExecutionSubgraph graph={null} focusNodeId="instrument:GBP" />)
    expect(screen.getByText('No manifest data available.')).toBeInTheDocument()
  })

  it('renders ReactFlow when graph has connected nodes', () => {
    render(<ExecutionSubgraph graph={buildTestGraph()} focusNodeId="instrument:GBP" />)
    expect(screen.getByTestId('react-flow')).toBeInTheDocument()
  })

  it('renders empty state when focus node has no connections', () => {
    const graph = buildTestGraph()
    render(<ExecutionSubgraph graph={graph} focusNodeId="instrument:USD" />)
    expect(screen.getByText('No connected elements found.')).toBeInTheDocument()
  })
})
