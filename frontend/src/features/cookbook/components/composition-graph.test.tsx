import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { CompositionGraph } from './composition-graph'
import type { CookbookItem, PatternMeta } from '../hooks/use-cookbook'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({})),
}))

// Mock @xyflow/react
vi.mock('@xyflow/react', () => {
  const Position = { Top: 'top', Bottom: 'bottom', Left: 'left', Right: 'right' }
  const BackgroundVariant = { Dots: 'dots' }

  function Handle() { return null }

  function ReactFlow({ nodes, edges, children }: { nodes: unknown[]; edges: unknown[]; children?: React.ReactNode }) {
    return (
      <div data-testid="react-flow" data-node-count={nodes.length} data-edge-count={edges.length}>
        {(nodes as { id: string; data: { label?: string } }[]).map((n) => (
          <div key={n.id} data-testid={`node-${n.id}`}>
            {n.data?.label ?? n.id}
          </div>
        ))}
        {children}
      </div>
    )
  }

  function Controls() { return <div data-testid="controls" /> }
  function Background() { return null }
  function MiniMap() { return <div data-testid="minimap" /> }

  return {
    ReactFlow,
    Controls,
    Background,
    MiniMap,
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

// Mock ELK for layout
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

const patterns: CookbookItem[] = [
  {
    name: 'energy-trading',
    type: 'registry:pattern',
    title: 'Energy Trading',
    categories: ['energy'],
    registryDependencies: ['fiat-settlement'],
    meta: {
      complexity: 7,
      composes_with: ['carbon-offset'],
      extends: [],
      conflicts_with: [],
    } satisfies PatternMeta,
  },
  {
    name: 'fiat-settlement',
    type: 'registry:pattern',
    title: 'Fiat Settlement',
    categories: ['foundation'],
    meta: {
      complexity: 3,
    } satisfies PatternMeta,
  },
  {
    name: 'carbon-offset',
    type: 'registry:pattern',
    title: 'Carbon Offset',
    categories: ['carbon'],
    meta: {
      complexity: 5,
      composes_with: ['energy-trading'],
    } satisfies PatternMeta,
  },
]

const mixedItems: CookbookItem[] = [
  ...patterns,
  {
    name: 'balance-card',
    type: 'registry:ui',
    title: 'Balance Card',
  },
]

function renderGraph(items: CookbookItem[]) {
  return render(
    <MemoryRouter>
      <CompositionGraph patterns={items} className="h-96" />
    </MemoryRouter>,
  )
}

describe('CompositionGraph', () => {
  it('renders ReactFlow container', () => {
    renderGraph(patterns)
    expect(screen.getByTestId('react-flow')).toBeInTheDocument()
  })

  it('renders controls and minimap', () => {
    renderGraph(patterns)
    expect(screen.getByTestId('controls')).toBeInTheDocument()
    expect(screen.getByTestId('minimap')).toBeInTheDocument()
  })

  it('renders filter sidebar', () => {
    renderGraph(patterns)
    expect(screen.getByLabelText('Filter by category')).toBeInTheDocument()
    expect(screen.getByLabelText('Filter by industry')).toBeInTheDocument()
  })

  it('renders edge legend', () => {
    renderGraph(patterns)
    expect(screen.getByText('Dependency')).toBeInTheDocument()
    expect(screen.getByText('Composes with')).toBeInTheDocument()
    expect(screen.getByText('Extends')).toBeInTheDocument()
    expect(screen.getByText('Conflicts')).toBeInTheDocument()
  })

  it('filters out UI components (only patterns)', () => {
    renderGraph(mixedItems)
    expect(screen.getByText('3 patterns')).toBeInTheDocument()
  })

  it('shows category filter options', () => {
    renderGraph(patterns)
    const select = screen.getByLabelText('Filter by category')
    expect(select).toBeInTheDocument()
    const options = select.querySelectorAll('option')
    expect(options.length).toBe(4) // All + 3 categories
  })

  it('renders with empty patterns', () => {
    renderGraph([])
    expect(screen.getByTestId('react-flow')).toBeInTheDocument()
    expect(screen.getByText('0 patterns')).toBeInTheDocument()
  })
})
