import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'
import type { ReactNode } from 'react'

const mockCreateNode = vi.fn()
const mockGetChildren = vi.fn().mockResolvedValue({ nodes: [] })

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    node: {
      createNode: mockCreateNode,
      getChildren: mockGetChildren,
    },
  })),
}))

import { CreateNodeDialog } from './create-node-dialog'

const mockRootNodes = [
  {
    id: 'parent-001',
    tenantId: 'tenant-001',
    nodeType: 'region',
    parentId: '',
    resolutionKey: 'region:parent-001',
    attributes: {},
    version: BigInt(1),
  },
]

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  })
}

function Wrapper({ children, queryClient }: { children: ReactNode; queryClient?: QueryClient }) {
  const qc = queryClient ?? makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <BrowserRouter>{children}</BrowserRouter>
      </TooltipProvider>
    </QueryClientProvider>
  )
}

function renderDialog(props: Partial<{
  open: boolean
  onOpenChange: (open: boolean) => void
  defaultParentId: string
  queryClient: QueryClient
}> = {}) {
  const {
    open = true,
    onOpenChange = vi.fn(),
    defaultParentId,
    queryClient,
  } = props

  return render(
    <Wrapper queryClient={queryClient}>
      <CreateNodeDialog
        open={open}
        onOpenChange={onOpenChange}
        defaultParentId={defaultParentId}
      />
    </Wrapper>,
  )
}

describe('CreateNodeDialog - rendering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetChildren.mockResolvedValue({ nodes: [] })
    mockCreateNode.mockResolvedValue({ node: { id: 'new-node-id' } })
  })

  it('renders dialog when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /create node/i })).toBeInTheDocument()
  })

  it('does not render when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders all form fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/^Code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/node type/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/parent node/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
    expect(screen.getByTestId('key-value-editor')).toBeInTheDocument()
  })

  it('renders cancel and create buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create node/i })).toBeInTheDocument()
  })

  it('parent node select has (Root Level) option', () => {
    renderDialog()
    expect(screen.getByRole('option', { name: /root level/i })).toBeInTheDocument()
  })
})

describe('CreateNodeDialog - validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetChildren.mockResolvedValue({ nodes: [] })
    mockCreateNode.mockResolvedValue({ node: { id: 'new-node-id' } })
  })

  it('shows error when code is empty', async () => {
    renderDialog()
    await userEvent.click(screen.getByRole('button', { name: /create node/i }))
    await waitFor(() => {
      expect(screen.getByText(/code is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when code is too short', async () => {
    renderDialog()
    await userEvent.type(screen.getByLabelText(/^Code/i), 'A')
    await userEvent.click(screen.getByRole('button', { name: /create node/i }))
    await waitFor(() => {
      expect(screen.getByText(/at least 2 characters/i)).toBeInTheDocument()
    })
  })

  it('shows error when code has invalid pattern - lowercase', async () => {
    renderDialog()
    await userEvent.type(screen.getByLabelText(/^Code/i), 'lowercase')
    await userEvent.click(screen.getByRole('button', { name: /create node/i }))
    await waitFor(() => {
      expect(screen.getByText(/must start with an uppercase letter/i)).toBeInTheDocument()
    })
  })

  it('shows error when code has invalid pattern - starts with digit', async () => {
    renderDialog()
    await userEvent.type(screen.getByLabelText(/^Code/i), '1INVALID')
    await userEvent.click(screen.getByRole('button', { name: /create node/i }))
    await waitFor(() => {
      expect(screen.getByText(/must start with an uppercase letter/i)).toBeInTheDocument()
    })
  })

  it('accepts valid code patterns with dots and hyphens', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.type(screen.getByLabelText(/^Code/i), 'EU.WEST-1')
    await user.type(screen.getByLabelText(/display name/i), 'EU West 1')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      // No validation errors about pattern
      expect(screen.queryByText(/must start with an uppercase letter/i)).not.toBeInTheDocument()
    })
  })

  it('shows error when display name is empty', async () => {
    renderDialog()
    await userEvent.type(screen.getByLabelText(/^Code/i), 'REGION_EU')
    await userEvent.click(screen.getByRole('button', { name: /create node/i }))
    await waitFor(() => {
      expect(screen.getByText(/display name is required/i)).toBeInTheDocument()
    })
  })
})

describe('CreateNodeDialog - parent node selection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockCreateNode.mockResolvedValue({ node: { id: 'new-node-id' } })
  })

  it('pre-selects parent node from defaultParentId prop', async () => {
    mockGetChildren.mockResolvedValue({ nodes: mockRootNodes })
    renderDialog({ defaultParentId: 'parent-001' })

    // Wait for the option to appear in the list
    await waitFor(() => {
      expect(screen.getByRole('option', { name: /parent-001/i })).toBeInTheDocument()
    })

    const select = screen.getByLabelText(/parent node/i) as HTMLSelectElement
    expect(select.value).toBe('parent-001')
  })

  it('shows (Root Level) option selected by default', () => {
    renderDialog()
    const select = screen.getByLabelText(/parent node/i) as HTMLSelectElement
    expect(select.value).toBe('')
  })

  it('shows root nodes as options when loaded', async () => {
    mockGetChildren.mockResolvedValue({ nodes: mockRootNodes })
    renderDialog()
    await waitFor(() => {
      expect(screen.getByRole('option', { name: /parent-001/i })).toBeInTheDocument()
    })
  })

  it('can deselect parent by choosing Root Level', async () => {
    const user = userEvent.setup()
    mockGetChildren.mockResolvedValue({ nodes: mockRootNodes })
    renderDialog({ defaultParentId: 'parent-001' })
    const select = screen.getByLabelText(/parent node/i)
    await user.selectOptions(select, '')
    expect((select as HTMLSelectElement).value).toBe('')
  })
})

describe('CreateNodeDialog - key-value editor integration', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetChildren.mockResolvedValue({ nodes: [] })
    mockCreateNode.mockResolvedValue({ node: { id: 'new-node-id' } })
  })

  it('renders key-value editor component', () => {
    renderDialog()
    expect(screen.getByTestId('key-value-editor')).toBeInTheDocument()
  })

  it('can add attributes via key-value editor', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    await user.type(screen.getByLabelText(/attribute key 1/i), 'zone')
    await user.type(screen.getByLabelText(/attribute value 1/i), 'UK')
    expect(screen.getByDisplayValue('zone')).toBeInTheDocument()
    expect(screen.getByDisplayValue('UK')).toBeInTheDocument()
  })

  it('can remove attributes via key-value editor', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    await user.click(screen.getByRole('button', { name: /remove attribute 1/i }))
    expect(screen.queryByLabelText(/attribute key 1/i)).not.toBeInTheDocument()
  })
})

describe('CreateNodeDialog - submission', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetChildren.mockResolvedValue({ nodes: [] })
    mockCreateNode.mockResolvedValue({ node: { id: 'new-node-id' } })
  })

  it('calls createNode with code and displayName in attributes', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/^Code/i), 'REGION_EU')
    await user.type(screen.getByLabelText(/display name/i), 'Europe')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      expect(mockCreateNode).toHaveBeenCalledOnce()
    })

    const callArgs = mockCreateNode.mock.calls[0][0]
    expect(callArgs).toMatchObject({
      nodeType: undefined,
      parentId: undefined,
    })
    // attributes should include code and displayName as protobuf Struct string values
    expect(callArgs.attributes?.fields?.code?.kind?.value).toBe('REGION_EU')
    expect(callArgs.attributes?.fields?.displayName?.kind?.value).toBe('Europe')
  })

  it('includes nodeType when provided', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/^Code/i), 'REGION_EU')
    await user.type(screen.getByLabelText(/display name/i), 'Europe')
    await user.type(screen.getByLabelText(/node type/i), 'region')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      expect(mockCreateNode).toHaveBeenCalledOnce()
    })

    const callArgs = mockCreateNode.mock.calls[0][0]
    expect(callArgs.nodeType).toBe('region')
  })

  it('includes parentId when parent selected', async () => {
    const user = userEvent.setup()
    mockGetChildren.mockResolvedValue({ nodes: mockRootNodes })
    renderDialog()

    await waitFor(() => {
      expect(screen.getByRole('option', { name: /parent-001/i })).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText(/^Code/i), 'REGION_EU')
    await user.type(screen.getByLabelText(/display name/i), 'Europe')
    await user.selectOptions(screen.getByLabelText(/parent node/i), 'parent-001')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      expect(mockCreateNode).toHaveBeenCalledOnce()
    })

    const callArgs = mockCreateNode.mock.calls[0][0]
    expect(callArgs.parentId).toBe('parent-001')
  })

  it('closes dialog on successful submission', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.type(screen.getByLabelText(/^Code/i), 'REGION_EU')
    await user.type(screen.getByLabelText(/display name/i), 'Europe')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('shows error alert on server failure', async () => {
    const user = userEvent.setup()
    mockCreateNode.mockRejectedValue(new Error('AlreadyExists: node already exists'))
    renderDialog()

    await user.type(screen.getByLabelText(/^Code/i), 'REGION_EU')
    await user.type(screen.getByLabelText(/display name/i), 'Europe')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })

  it('invalidates node-roots query on success', async () => {
    const user = userEvent.setup()
    const qc = makeQueryClient()
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
    renderDialog({ queryClient: qc })

    await user.type(screen.getByLabelText(/^Code/i), 'REGION_EU')
    await user.type(screen.getByLabelText(/display name/i), 'Europe')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: ['node-roots'] }),
      )
    })
  })

  it('invalidates node-children query for parent on success', async () => {
    const user = userEvent.setup()
    mockGetChildren.mockResolvedValue({ nodes: mockRootNodes })
    const qc = makeQueryClient()
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
    renderDialog({ defaultParentId: 'parent-001', queryClient: qc })

    await user.type(screen.getByLabelText(/^Code/i), 'ZONE_UK')
    await user.type(screen.getByLabelText(/display name/i), 'UK')
    await user.click(screen.getByRole('button', { name: /create node/i }))

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: ['node-children', 'parent-001'] }),
      )
    })
  })
})

describe('CreateNodeDialog - cancel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetChildren.mockResolvedValue({ nodes: [] })
    mockCreateNode.mockResolvedValue({ node: { id: 'new-node-id' } })
  })

  it('calls onOpenChange(false) when cancel clicked', async () => {
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })
    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
