import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ActivityFeed, type ActivityItem } from './activity-feed'

const mockItems: ActivityItem[] = [
  {
    id: '1',
    type: 'payment',
    title: 'Payment order created',
    description: 'PO-001 for £100.00',
    timestamp: { seconds: BigInt(1700000000), nanos: 0 },
    status: 'INITIATED',
  },
  {
    id: '2',
    type: 'account',
    title: 'Account opened',
    description: 'Current account ACC-001',
    timestamp: { seconds: BigInt(1699999000), nanos: 0 },
    status: 'ACTIVE',
  },
]

describe('ActivityFeed', () => {
  it('renders empty state when no items', () => {
    render(<ActivityFeed items={[]} />)

    expect(screen.getByText('No recent activity')).toBeInTheDocument()
  })

  it('renders activity items', () => {
    render(<ActivityFeed items={mockItems} />)

    expect(screen.getByText('Payment order created')).toBeInTheDocument()
    expect(screen.getByText('Account opened')).toBeInTheDocument()
  })

  it('renders item descriptions', () => {
    render(<ActivityFeed items={mockItems} />)

    expect(screen.getByText('PO-001 for £100.00')).toBeInTheDocument()
    expect(screen.getByText('Current account ACC-001')).toBeInTheDocument()
  })

  it('renders loading skeleton when isLoading is true', () => {
    render(<ActivityFeed items={[]} isLoading />)

    expect(screen.getAllByTestId('activity-skeleton')).toHaveLength(5)
  })

  it('renders status badges for items', () => {
    render(<ActivityFeed items={mockItems} />)

    expect(screen.getByText('INITIATED')).toBeInTheDocument()
    expect(screen.getByText('ACTIVE')).toBeInTheDocument()
  })
})
