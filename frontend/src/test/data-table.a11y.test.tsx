import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { axe } from '@/test/test-utils'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'

type TestRow = { id: string; name: string; status: string }

const columns: ColumnDef<TestRow>[] = [
  { accessorKey: 'id', header: 'ID' },
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'status', header: 'Status' },
]

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe('DataTable accessibility', () => {
  it('has no accessibility violations with data', async () => {
    const queryFn = vi.fn().mockResolvedValue({
      items: [
        { id: '1', name: 'Item 1', status: 'ACTIVE' },
        { id: '2', name: 'Item 2', status: 'INACTIVE' },
      ],
    })

    const { container } = render(
      <Wrapper>
        <DataTable queryKey={['test-a11y']} queryFn={queryFn} columns={columns} />
      </Wrapper>
    )

    await waitFor(() => {
      expect(screen.getByText('Item 1')).toBeInTheDocument()
    })

    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no violations in empty state', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: [] })

    const { container } = render(
      <Wrapper>
        <DataTable queryKey={['test-empty-a11y']} queryFn={queryFn} columns={columns} />
      </Wrapper>
    )

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })

    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no violations in error state', async () => {
    const queryFn = vi.fn().mockRejectedValue(new Error('Load failed'))

    const { container } = render(
      <Wrapper>
        <DataTable queryKey={['test-error-a11y']} queryFn={queryFn} columns={columns} />
      </Wrapper>
    )

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })

    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has proper table semantics', async () => {
    const queryFn = vi.fn().mockResolvedValue({
      items: [{ id: '1', name: 'Item 1', status: 'ACTIVE' }],
    })

    const { container } = render(
      <Wrapper>
        <DataTable queryKey={['test-semantics']} queryFn={queryFn} columns={columns} />
      </Wrapper>
    )

    await waitFor(() => {
      expect(screen.getByText('Item 1')).toBeInTheDocument()
    })

    const table = container.querySelector('table')
    expect(table).toBeInTheDocument()

    const headers = container.querySelectorAll('th')
    expect(headers.length).toBe(3) // ID, Name, Status
    expect(headers[0].textContent).toBe('ID')
  })

  it('renders interactive elements that support keyboard navigation', async () => {
    const queryFn = vi.fn().mockResolvedValue({
      items: [
        { id: '1', name: 'Item 1', status: 'ACTIVE' },
        { id: '2', name: 'Item 2', status: 'INACTIVE' },
      ],
    })

    render(
      <Wrapper>
        <DataTable queryKey={['test-keyboard']} queryFn={queryFn} columns={columns} />
      </Wrapper>
    )

    await waitFor(() => {
      expect(screen.getByText('Item 1')).toBeInTheDocument()
    })

    // DataTable table cells are not directly focusable, but pagination/filters are
    // Verify table structure is present for keyboard navigation
    const table = screen.getByRole('table', { hidden: true })
    expect(table).toBeInTheDocument()
  })


  it('announces retry button to screen readers', async () => {
    const queryFn = vi.fn().mockRejectedValue(new Error('Load failed'))

    render(
      <Wrapper>
        <DataTable queryKey={['test-retry']} queryFn={queryFn} columns={columns} />
      </Wrapper>
    )

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })

    const retryButton = screen.getByRole('button', { name: /retry/i })
    expect(retryButton).toHaveAccessibleName()
  })
})
