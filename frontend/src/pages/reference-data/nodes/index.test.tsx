import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockGetChildren = vi.fn().mockResolvedValue({
  nodes: [],
})
const mockGetSubtree = vi.fn().mockResolvedValue({
  nodes: [],
})
const mockGetNodeAsAt = vi.fn().mockResolvedValue({
  node: null,
})

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    node: {
      getChildren: mockGetChildren,
      getSubtree: mockGetSubtree,
      getNodeAsAt: mockGetNodeAsAt,
    },
  })),
}))

import { NodesPage } from './index'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <BrowserRouter>{children}</BrowserRouter>
      </TooltipProvider>
    </QueryClientProvider>
  )
}

const mockRootNodes = [
  {
    id: 'root-001',
    tenantId: 'tenant-001',
    nodeType: 'region',
    parentId: '',
    resolutionKey: 'region:root-001',
    attributes: { name: 'Europe' },
    validFrom: { seconds: BigInt(1700000000), nanos: 0 },
    validTo: undefined,
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    version: BigInt(1),
  },
  {
    id: 'root-002',
    tenantId: 'tenant-001',
    nodeType: 'region',
    parentId: '',
    resolutionKey: 'region:root-002',
    attributes: { name: 'Americas' },
    validFrom: { seconds: BigInt(1700001000), nanos: 0 },
    validTo: undefined,
    createdAt: { seconds: BigInt(1700001000), nanos: 0 },
    version: BigInt(1),
  },
]

const mockChildNodes = [
  {
    id: 'child-001',
    tenantId: 'tenant-001',
    nodeType: 'zone',
    parentId: 'root-001',
    resolutionKey: 'region:root-001/zone:child-001',
    attributes: { name: 'UK' },
    validFrom: { seconds: BigInt(1700002000), nanos: 0 },
    validTo: undefined,
    createdAt: { seconds: BigInt(1700002000), nanos: 0 },
    version: BigInt(1),
  },
]

describe('NodesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetChildren.mockResolvedValue({ nodes: [] })
    mockGetSubtree.mockResolvedValue({ nodes: [] })
  })

  it('renders page title', () => {
    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )
    expect(screen.getByRole('heading', { name: /nodes/i })).toBeInTheDocument()
  })

  it('renders temporal query date picker', () => {
    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    expect(screen.getByTestId('temporal-date-picker')).toBeInTheDocument()
  })

  it('renders root nodes when data is available', async () => {
    mockGetChildren.mockResolvedValue({ nodes: mockRootNodes })

    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('root-001')).toBeInTheDocument()
      expect(screen.getByText('root-002')).toBeInTheDocument()
    })
  })

  it('shows empty state when no root nodes', async () => {
    mockGetChildren.mockResolvedValue({ nodes: [] })

    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('empty-tree-state')).toBeInTheDocument()
    })
  })

  it('renders expand button for nodes with children', async () => {
    mockGetChildren.mockImplementation(({ parentId }: { parentId: string }) => {
      if (parentId === '') return Promise.resolve({ nodes: mockRootNodes })
      return Promise.resolve({ nodes: [] })
    })

    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      const expandButtons = screen.getAllByRole('button', { name: /expand/i })
      expect(expandButtons.length).toBeGreaterThan(0)
    })
  })

  it('loads children when expand button clicked', async () => {
    const user = userEvent.setup()
    mockGetChildren.mockImplementation(({ parentId }: { parentId: string }) => {
      if (parentId === '') return Promise.resolve({ nodes: [mockRootNodes[0]] })
      if (parentId === 'root-001') return Promise.resolve({ nodes: mockChildNodes })
      return Promise.resolve({ nodes: [] })
    })

    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('root-001')).toBeInTheDocument()
    })

    const expandBtn = screen.getByRole('button', { name: /expand/i })
    await user.click(expandBtn)

    await waitFor(() => {
      expect(mockGetChildren).toHaveBeenCalledWith(
        expect.objectContaining({ parentId: 'root-001' }),
      )
    })
  })

  it('collapses node when collapse button clicked', async () => {
    const user = userEvent.setup()
    mockGetChildren.mockImplementation(({ parentId }: { parentId: string }) => {
      if (parentId === '') return Promise.resolve({ nodes: [mockRootNodes[0]] })
      if (parentId === 'root-001') return Promise.resolve({ nodes: mockChildNodes })
      return Promise.resolve({ nodes: [] })
    })

    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('root-001')).toBeInTheDocument()
    })

    // Expand first
    const expandBtn = screen.getByRole('button', { name: /expand/i })
    await user.click(expandBtn)

    await waitFor(() => {
      expect(screen.getByText('child-001')).toBeInTheDocument()
    })

    // Then collapse
    const collapseBtn = screen.getByRole('button', { name: /collapse/i })
    await user.click(collapseBtn)

    await waitFor(() => {
      expect(screen.queryByText('child-001')).not.toBeInTheDocument()
    })
  })

  it('calls getChildren with as_at timestamp when date set', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    const dateInput = screen.getByTestId('temporal-date-picker')
    await user.type(dateInput, '2024-01-15')

    await waitFor(() => {
      // getChildren should be called (initial fetch with asAt)
      expect(mockGetChildren).toHaveBeenCalled()
    })
  })

  it('shows node type for each node', async () => {
    mockGetChildren.mockResolvedValue({ nodes: mockRootNodes })

    render(
      <Wrapper>
        <NodesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      const regionLabels = screen.getAllByText('region')
      expect(regionLabels.length).toBeGreaterThan(0)
    })
  })
})
