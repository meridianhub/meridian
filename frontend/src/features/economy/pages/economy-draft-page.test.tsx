import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { EconomyDraftPage } from './economy-draft-page'

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return { ...actual, useNavigate: () => mockNavigate }
})

// Mock the draft store to control state in tests
const mockAddChange = vi.fn()
const mockRemoveChange = vi.fn()
const mockClearAll = vi.fn()
const mockSetBaseVersion = vi.fn()

let mockStoreState = {
  baseVersion: null as string | null,
  changes: [] as Array<{
    id: string
    type: 'add_saga' | 'override_default' | 'add_instrument' | 'modify_account_type'
    description: string
    patch: Record<string, unknown>
    createdAt: number
  }>,
  addChange: mockAddChange,
  removeChange: mockRemoveChange,
  clearAll: mockClearAll,
  setBaseVersion: mockSetBaseVersion,
}

vi.mock('../lib/draft-manager', () => ({
  useDraftStore: (selector: (s: typeof mockStoreState) => unknown) => selector(mockStoreState),
  mergeDraftChanges: vi.fn(),
}))

function renderPage() {
  return render(
    <MemoryRouter>
      <EconomyDraftPage />
    </MemoryRouter>
  )
}

describe('EconomyDraftPage', () => {
  beforeEach(() => {
    mockNavigate.mockClear()
    mockRemoveChange.mockClear()
    mockClearAll.mockClear()
    mockStoreState = {
      baseVersion: null,
      changes: [],
      addChange: mockAddChange,
      removeChange: mockRemoveChange,
      clearAll: mockClearAll,
      setBaseVersion: mockSetBaseVersion,
    }
  })

  it('renders page title', () => {
    renderPage()
    expect(screen.getByText('Draft Changes')).toBeInTheDocument()
  })

  it('shows empty state when no draft changes', () => {
    renderPage()
    expect(screen.getByTestId('draft-empty-state')).toBeInTheDocument()
  })

  it('does not show empty state when changes exist', () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        {
          id: 'c1',
          type: 'add_saga',
          description: 'Add payment saga',
          patch: {},
          createdAt: 1700000000000,
        },
      ],
    }
    renderPage()
    expect(screen.queryByTestId('draft-empty-state')).not.toBeInTheDocument()
  })

  it('renders change descriptions', () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        {
          id: 'c1',
          type: 'add_saga',
          description: 'Add payment saga',
          patch: {},
          createdAt: 1700000000000,
        },
        {
          id: 'c2',
          type: 'add_instrument',
          description: 'Add GBP instrument',
          patch: {},
          createdAt: 1700000001000,
        },
      ],
    }
    renderPage()
    expect(screen.getByText('Add payment saga')).toBeInTheDocument()
    expect(screen.getByText('Add GBP instrument')).toBeInTheDocument()
  })

  it('renders Revert button for each change', () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        { id: 'c1', type: 'add_saga', description: 'A', patch: {}, createdAt: 1700000000000 },
        { id: 'c2', type: 'add_instrument', description: 'B', patch: {}, createdAt: 1700000001000 },
      ],
    }
    renderPage()
    const revertButtons = screen.getAllByRole('button', { name: /revert/i })
    expect(revertButtons).toHaveLength(2)
  })

  it('calls removeChange with correct id when Revert is clicked', async () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        { id: 'c1', type: 'add_saga', description: 'A', patch: {}, createdAt: 1700000000000 },
      ],
    }
    renderPage()
    await userEvent.click(screen.getByRole('button', { name: /revert/i }))
    expect(mockRemoveChange).toHaveBeenCalledWith('c1')
  })

  it('renders Clear All button when changes exist', () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        { id: 'c1', type: 'add_saga', description: 'A', patch: {}, createdAt: 1700000000000 },
      ],
    }
    renderPage()
    expect(screen.getByRole('button', { name: /clear all/i })).toBeInTheDocument()
  })

  it('calls clearAll when Clear All is clicked', async () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        { id: 'c1', type: 'add_saga', description: 'A', patch: {}, createdAt: 1700000000000 },
      ],
    }
    renderPage()
    await userEvent.click(screen.getByRole('button', { name: /clear all/i }))
    expect(mockClearAll).toHaveBeenCalled()
  })

  it('renders Review Draft button when changes exist', () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        { id: 'c1', type: 'add_saga', description: 'A', patch: {}, createdAt: 1700000000000 },
      ],
    }
    renderPage()
    expect(screen.getByRole('button', { name: /review draft/i })).toBeInTheDocument()
  })

  it('navigates to /economy/edit with reviewDraft state when Review Draft is clicked', async () => {
    mockStoreState = {
      ...mockStoreState,
      changes: [
        { id: 'c1', type: 'add_saga', description: 'A', patch: {}, createdAt: 1700000000000 },
      ],
    }
    renderPage()
    await userEvent.click(screen.getByRole('button', { name: /review draft/i }))
    expect(mockNavigate).toHaveBeenCalledWith('/economy/edit', { state: { reviewDraft: true } })
  })

  it('does not show Review Draft and Clear All buttons when no changes', () => {
    renderPage()
    expect(screen.queryByRole('button', { name: /review draft/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /clear all/i })).not.toBeInTheDocument()
  })
})
