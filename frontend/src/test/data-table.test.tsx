import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

describe('DataTable - loading state', () => {
  it('renders skeleton rows matching pageSize while loading', async () => {
    const queryFn = vi.fn(() => new Promise(() => {})) // never resolves

    render(
      <Wrapper>
        <DataTable
          queryKey={['test']}
          queryFn={queryFn}
          columns={columns}
          pageSize={5}
        />
      </Wrapper>,
    )

    const skeletons = screen.getAllByTestId('skeleton-row')
    expect(skeletons).toHaveLength(5)
  })
})

describe('DataTable - error state', () => {
  it('renders error message and retry button on failure', async () => {
    const queryFn = vi.fn().mockRejectedValue(new Error('Network error'))
    const { rerender } = render(
      <Wrapper>
        <DataTable queryKey={['test-err']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })
    expect(screen.getByText(/failed to load data/i)).toBeInTheDocument()
    rerender(<div />)
  })

  it('retry button calls refetch', async () => {
    const queryFn = vi.fn().mockRejectedValue(new Error('fail'))
    render(
      <Wrapper>
        <DataTable queryKey={['test-err2']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument(),
    )

    const retryButton = screen.getByRole('button', { name: /retry/i })
    await userEvent.click(retryButton)

    await waitFor(() => {
      expect(queryFn).toHaveBeenCalledTimes(2)
    })
  })
})

describe('DataTable - empty state', () => {
  it('renders empty state when items array is empty', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <DataTable queryKey={['test-empty']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('shows "No results" text in empty state', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <DataTable queryKey={['test-empty2']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText(/no results/i)).toBeInTheDocument()
    })
  })
})

describe('DataTable - data rendering', () => {
  const rows: TestRow[] = [
    { id: 'r1', name: 'Alice', status: 'active' },
    { id: 'r2', name: 'Bob', status: 'inactive' },
  ]

  it('renders column headers', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: rows })

    render(
      <Wrapper>
        <DataTable queryKey={['test-cols']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'ID' })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument()
      expect(
        screen.getByRole('columnheader', { name: 'Status' }),
      ).toBeInTheDocument()
    })
  })

  it('renders data rows', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: rows })

    render(
      <Wrapper>
        <DataTable queryKey={['test-rows']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('Alice')).toBeInTheDocument()
      expect(screen.getByText('Bob')).toBeInTheDocument()
    })
  })

  it('calls onRowClick with row data when a row is clicked', async () => {
    const onRowClick = vi.fn()
    const queryFn = vi.fn().mockResolvedValue({ items: rows })

    render(
      <Wrapper>
        <DataTable
          queryKey={['test-click']}
          queryFn={queryFn}
          columns={columns}
          onRowClick={onRowClick}
        />
      </Wrapper>,
    )

    await waitFor(() => expect(screen.getByText('Alice')).toBeInTheDocument())
    await userEvent.click(screen.getByText('Alice'))

    expect(onRowClick).toHaveBeenCalledWith(rows[0])
  })

  it('renders custom cell renderer when provided', async () => {
    const customColumns: ColumnDef<TestRow>[] = [
      {
        accessorKey: 'status',
        header: 'Status',
        cell: ({ row }) => (
          <span data-testid="custom-cell">{row.original.status.toUpperCase()}</span>
        ),
      },
    ]
    const queryFn = vi.fn().mockResolvedValue({ items: rows })

    render(
      <Wrapper>
        <DataTable queryKey={['test-custom']} queryFn={queryFn} columns={customColumns} />
      </Wrapper>,
    )

    await waitFor(() => {
      const cells = screen.getAllByTestId('custom-cell')
      expect(cells[0]).toHaveTextContent('ACTIVE')
    })
  })

  it('has proper table semantics (role=table, columnheader, cell)', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: rows })

    render(
      <Wrapper>
        <DataTable queryKey={['test-a11y']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('table')).toBeInTheDocument()
    })
    expect(screen.getAllByRole('columnheader')).toHaveLength(3)
    expect(screen.getAllByRole('cell').length).toBeGreaterThan(0)
  })
})

describe('DataTable - cursor-based pagination', () => {
  it('does not show next button when no nextPageToken', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: [{ id: '1', name: 'A', status: 's' }] })

    render(
      <Wrapper>
        <DataTable queryKey={['test-pg1']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() => expect(screen.getByText('A')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /next/i })).not.toBeInTheDocument()
  })

  it('shows next page button when nextPageToken is present', async () => {
    const queryFn = vi
      .fn()
      .mockResolvedValue({ items: [{ id: '1', name: 'A', status: 's' }], nextPageToken: 'tok1' })

    render(
      <Wrapper>
        <DataTable queryKey={['test-pg2']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument(),
    )
  })

  it('passes pageToken to queryFn when next page clicked', async () => {
    const queryFn = vi
      .fn()
      .mockResolvedValueOnce({
        items: [{ id: '1', name: 'A', status: 's' }],
        nextPageToken: 'tok1',
      })
      .mockResolvedValueOnce({ items: [{ id: '2', name: 'B', status: 's' }] })

    render(
      <Wrapper>
        <DataTable queryKey={['test-pg3']} queryFn={queryFn} columns={columns} pageSize={25} />
      </Wrapper>,
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument(),
    )

    await userEvent.click(screen.getByRole('button', { name: /next/i }))

    await waitFor(() => {
      expect(queryFn).toHaveBeenCalledWith(
        expect.objectContaining({ pageToken: 'tok1', pageSize: 25 }),
      )
    })
  })

  it('shows previous page button after navigating to page 2', async () => {
    const queryFn = vi
      .fn()
      .mockResolvedValueOnce({
        items: [{ id: '1', name: 'A', status: 's' }],
        nextPageToken: 'tok1',
      })
      .mockResolvedValueOnce({ items: [{ id: '2', name: 'B', status: 's' }] })

    render(
      <Wrapper>
        <DataTable queryKey={['test-pg4']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument(),
    )

    await userEvent.click(screen.getByRole('button', { name: /next/i }))

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /previous/i })).toBeInTheDocument(),
    )
  })

  it('previous page resets to first page (cursor pagination)', async () => {
    const queryFn = vi
      .fn()
      .mockResolvedValueOnce({
        items: [{ id: '1', name: 'A', status: 's' }],
        nextPageToken: 'tok1',
      })
      .mockResolvedValueOnce({ items: [{ id: '2', name: 'B', status: 's' }] })

    render(
      <Wrapper>
        <DataTable queryKey={['test-pg5']} queryFn={queryFn} columns={columns} />
      </Wrapper>,
    )

    // First page: A is shown, next button is visible
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument(),
    )
    expect(screen.getByText('A')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /next/i }))

    // Second page: B is shown, previous button is visible
    await waitFor(() => expect(screen.getByText('B')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /previous/i })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /previous/i }))

    // After going back: previous button disappears (we're on first page again)
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: /previous/i })).not.toBeInTheDocument(),
    )

    // First page data is restored (from cache)
    expect(screen.getByText('A')).toBeInTheDocument()
  })
})

describe('DataTable - filter system', () => {
  it('renders filter inputs when filters config is provided', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <DataTable
          queryKey={['test-filter1']}
          queryFn={queryFn}
          columns={columns}
          filters={[{ field: 'status', label: 'Status', type: 'text' }]}
        />
      </Wrapper>,
    )

    expect(screen.getByPlaceholderText(/filter by status/i)).toBeInTheDocument()
  })

  it('passes active filters to queryFn', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <DataTable
          queryKey={['test-filter2']}
          queryFn={queryFn}
          columns={columns}
          filters={[{ field: 'status', label: 'Status', type: 'text' }]}
        />
      </Wrapper>,
    )

    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1))

    const input = screen.getByPlaceholderText(/filter by status/i)
    await userEvent.type(input, 'active')

    await waitFor(() => {
      const lastCall = queryFn.mock.calls[queryFn.mock.calls.length - 1][0]
      expect(lastCall.filters).toMatchObject({ status: 'active' })
    })
  })

  it('resets pagination when filters change', async () => {
    const queryFn = vi
      .fn()
      .mockResolvedValue({ items: [{ id: '1', name: 'A', status: 's' }], nextPageToken: 'tok1' })

    render(
      <Wrapper>
        <DataTable
          queryKey={['test-filter3']}
          queryFn={queryFn}
          columns={columns}
          filters={[{ field: 'name', label: 'Name', type: 'text' }]}
        />
      </Wrapper>,
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument(),
    )

    await userEvent.click(screen.getByRole('button', { name: /next/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /previous/i })).toBeInTheDocument(),
    )

    // Changing filter should reset pagination
    const input = screen.getByPlaceholderText(/filter by name/i)
    await userEvent.type(input, 'A')

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /previous/i })).not.toBeInTheDocument()
    })
  })

  it('renders select filter type', async () => {
    const queryFn = vi.fn().mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <DataTable
          queryKey={['test-filter4']}
          queryFn={queryFn}
          columns={columns}
          filters={[
            {
              field: 'status',
              label: 'Status',
              type: 'select',
              options: [
                { label: 'Active', value: 'active' },
                { label: 'Inactive', value: 'inactive' },
              ],
            },
          ]}
        />
      </Wrapper>,
    )

    expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
  })
})
