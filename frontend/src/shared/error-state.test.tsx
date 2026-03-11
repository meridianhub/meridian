import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ErrorState } from './error-state'

describe('ErrorState', () => {
  it('renders default title and message', () => {
    render(<ErrorState />)
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Failed to load')
    expect(screen.getByText('There was a problem loading this content. Please try again.')).toBeInTheDocument()
  })

  it('renders custom title and message', () => {
    render(<ErrorState title="Custom Error" message="Something broke" />)
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Custom Error')
    expect(screen.getByText('Something broke')).toBeInTheDocument()
  })

  it('renders retry button when onRetry provided', () => {
    render(<ErrorState onRetry={() => {}} />)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('does not render retry button when onRetry not provided', () => {
    render(<ErrorState />)
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('calls onRetry when retry button clicked', async () => {
    const user = userEvent.setup()
    const onRetry = vi.fn()
    render(<ErrorState onRetry={onRetry} />)
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('applies custom className', () => {
    const { container } = render(<ErrorState className="custom-class" />)
    expect(container.firstChild).toHaveClass('custom-class')
  })
})
