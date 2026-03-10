import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

  it('renders error state with icon and message', () => {
    render(<StatCard title="Accounts" error />)

    expect(screen.getByTestId('stat-card-error')).toBeInTheDocument()
    expect(screen.getByText('Failed to load')).toBeInTheDocument()
  })

  it('renders retry button in error state when onRetry is provided', () => {
    const onRetry = vi.fn()
    render(<StatCard title="Accounts" error onRetry={onRetry} />)

    expect(screen.getByRole('button', { name: /retry accounts/i })).toBeInTheDocument()
  })

  it('calls onRetry when retry button is clicked', async () => {
    const user = userEvent.setup()
    const onRetry = vi.fn()
    render(<StatCard title="Accounts" error onRetry={onRetry} />)

    await user.click(screen.getByRole('button', { name: /retry accounts/i }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('does not render retry button when onRetry is not provided', () => {
    render(<StatCard title="Accounts" error />)

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('hides description when in error state', () => {
    render(<StatCard title="Accounts" error description="Active accounts" />)

    expect(screen.queryByText('Active accounts')).not.toBeInTheDocument()
  })

  it('renders icon when provided', () => {
    const icon = <span data-testid="test-icon">icon</span>
    render(<StatCard title="Accounts" value={5} icon={icon} />)

    expect(screen.getByTestId('test-icon')).toBeInTheDocument()
  })
})
