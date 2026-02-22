import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StatCard } from './stat-card'

describe('StatCard', () => {
  it('renders loading skeleton when isLoading is true', () => {
    render(<StatCard title="Accounts" isLoading />)

    expect(screen.getByTestId('stat-card-skeleton')).toBeInTheDocument()
    expect(screen.getByText('Accounts')).toBeInTheDocument()
  })

  it('renders value when loaded', () => {
    render(<StatCard title="Accounts" value={42} />)

    expect(screen.getByText('42')).toBeInTheDocument()
    expect(screen.getByText('Accounts')).toBeInTheDocument()
  })

  it('renders zero value correctly', () => {
    render(<StatCard title="Payments" value={0} />)

    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('renders with "recent" qualifier when showRecentQualifier is true', () => {
    render(<StatCard title="Accounts" value={5} showRecentQualifier />)

    expect(screen.getByText('Accounts')).toBeInTheDocument()
    expect(screen.getByText('recent')).toBeInTheDocument()
  })

  it('does not render "recent" qualifier by default', () => {
    render(<StatCard title="Accounts" value={5} />)

    expect(screen.queryByText('recent')).not.toBeInTheDocument()
  })

  it('renders description when provided', () => {
    render(<StatCard title="Accounts" value={10} description="Active accounts" />)

    expect(screen.getByText('Active accounts')).toBeInTheDocument()
  })

  it('renders error state when error is true', () => {
    render(<StatCard title="Accounts" error />)

    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('renders icon when provided', () => {
    const icon = <span data-testid="test-icon">icon</span>
    render(<StatCard title="Accounts" value={5} icon={icon} />)

    expect(screen.getByTestId('test-icon')).toBeInTheDocument()
  })
})
