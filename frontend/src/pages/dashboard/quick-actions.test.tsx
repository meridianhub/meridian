import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QuickActions, type QuickAction } from './quick-actions'

const mockActions: QuickAction[] = [
  {
    id: 'new-payment',
    label: 'New Payment',
    description: 'Create a payment order',
    onClick: vi.fn(),
  },
  {
    id: 'view-accounts',
    label: 'View Accounts',
    description: 'Browse all accounts',
    onClick: vi.fn(),
  },
]

describe('QuickActions', () => {
  it('renders all action buttons', () => {
    render(<QuickActions actions={mockActions} />)

    expect(screen.getByText('New Payment')).toBeInTheDocument()
    expect(screen.getByText('View Accounts')).toBeInTheDocument()
  })

  it('renders action descriptions', () => {
    render(<QuickActions actions={mockActions} />)

    expect(screen.getByText('Create a payment order')).toBeInTheDocument()
    expect(screen.getByText('Browse all accounts')).toBeInTheDocument()
  })

  it('calls onClick handler when button clicked', async () => {
    const user = userEvent.setup()
    render(<QuickActions actions={mockActions} />)

    await user.click(screen.getByText('New Payment'))

    expect(mockActions[0].onClick).toHaveBeenCalledOnce()
  })

  it('renders empty state when no actions provided', () => {
    render(<QuickActions actions={[]} />)

    expect(screen.getByText('No quick actions available')).toBeInTheDocument()
  })
})
